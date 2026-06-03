import asyncio
import json
import logging
import re

import httpx
from duckduckgo_search import DDGS

logger = logging.getLogger(__name__)

# Subset of repos for the example, would normally be larger
ENTITY_REPOS = {
    "stripe": "stripe/stripe-python",
    "sendgrid": "sendgrid/sendgrid-python",
    "twilio": "twilio/twilio-python",
    "redis": "redis/redis-py",
    "prisma": "prisma/prisma-client-py",
    "sqlalchemy": "sqlalchemy/sqlalchemy",
    "fastapi": "fastapi/fastapi",
    "nextjs": "vercel/next.js",
    "next.js": "vercel/next.js",
    "express": "expressjs/express",
    "drizzle": "drizzle-team/drizzle-orm",
    "supabase": "supabase/supabase-py",
    "firebase": "firebase/firebase-admin-python",
    "celery": "celery/celery",
    "pydantic": "pydantic/pydantic",
    "alembic": "sqlalchemy/alembic",
    "hono": "honojs/hono",
    "fiber": "gofiber/fiber",
}

DEEPWIKI_URL = "https://mcp.deepwiki.com/mcp"

# Shared HTTP client for DeepWiki calls — reused across all research queries
# to avoid creating/destroying client pools per call.
_deepwiki_client: httpx.AsyncClient | None = None


async def _get_deepwiki_client() -> httpx.AsyncClient:
    """Return a shared HTTP client for DeepWiki calls."""
    global _deepwiki_client
    if _deepwiki_client is None or _deepwiki_client.is_closed:
        _deepwiki_client = httpx.AsyncClient(
            timeout=httpx.Timeout(connect=10.0, read=30.0, write=10.0, pool=30.0)
        )
    return _deepwiki_client


def _extract_entities(manifest: dict) -> list[str]:
    """Word-boundary match manifest text against the known-entity table.

    Word boundaries avoid false positives like 'redis' inside 'credis' and
    handle entities embedded in larger strings.
    """
    text = json.dumps(manifest, default=str).lower()
    found = []
    for entity in ENTITY_REPOS:
        # \b doesn't treat '.' as a boundary, so anchor on non-alnum surroundings.
        pattern = r"(?<![a-z0-9])" + re.escape(entity) + r"(?![a-z0-9])"
        if re.search(pattern, text):
            found.append(entity)
    return found


async def resolve_research(
    manifest: dict,
    language: str,
    groq_client=None,  # optional GroqClient
    max_queries: int = 5,
    timeout: float = 15.0,
) -> dict:
    found_entities = _extract_entities(manifest)
    # de-dupe by repo
    seen_repos: set[str] = set()
    unique_entities = []
    for e in found_entities:
        repo = ENTITY_REPOS[e]
        if repo not in seen_repos:
            seen_repos.add(repo)
            unique_entities.append(e)
    found_entities = unique_entities[:max_queries]

    findings: list[str] = []
    queries: list[str] = []

    for entity in found_entities:
        repo = ENTITY_REPOS[entity]

        if groq_client is not None:
            # Fire Compound web search + DeepWiki in PARALLEL
            compound_query = f"latest {entity} {language} best practices, APIs, and patterns"
            queries.append(f"Compound: {compound_query}")
            queries.append(f"DeepWiki: {repo}")

            try:
                compound_task = asyncio.wait_for(
                    groq_client.compound_search(compound_query),
                    timeout=timeout,
                )
                deepwiki_task = asyncio.wait_for(
                    deepwiki_ask(repo, "What are the core usage patterns and key APIs?"),
                    timeout=timeout,
                )

                results = await asyncio.gather(
                    compound_task, deepwiki_task,
                    return_exceptions=True
                )

                compound_result = results[0] if not isinstance(results[0], Exception) else ""
                deepwiki_result = results[1] if not isinstance(results[1], Exception) else ""

                # Synthesize both sources
                combined = []
                if compound_result:
                    combined.append(f"[Web search - {entity}]: {compound_result[:800]}")
                if deepwiki_result:
                    combined.append(f"[DeepWiki - {entity}]: {deepwiki_result[:800]}")

                if combined:
                    findings.append("\n".join(combined))
                    continue
            except Exception as e:
                logger.warning(f"Parallel research failed for {entity}: {e}")

        # Fallback: DeepWiki only, then DDG
        try:
            res = await asyncio.wait_for(
                deepwiki_ask(repo, "What are the core usage patterns and key APIs?"),
                timeout=timeout,
            )
            if res:
                findings.append(f"[{entity} (DeepWiki)]: {res}")
                continue
        except Exception as e:
            logger.warning(f"DeepWiki failed for {repo}: {e}")

        # Final fallback: DDG
        try:
            res = await asyncio.wait_for(
                asyncio.to_thread(ddg_search, f"{entity} {language} code examples", 3),
                timeout=timeout,
            )
            if res:
                findings.append(f"[{entity} (DDG)]: {' '.join(res)}")
        except Exception as e:
            logger.warning(f"DDG failed for {entity}: {e}")

    combined = "\n".join(findings)
    return {
        "queries": queries,
        "findings": findings,
        "approx_tokens": max(1, len(combined) // 4) if combined else 0,
    }


def _parse_mcp_response(resp: httpx.Response) -> dict | None:
    """MCP Streamable HTTP may answer as application/json OR text/event-stream."""
    ctype = resp.headers.get("content-type", "")
    if "text/event-stream" in ctype:
        # Concatenate the JSON payloads from `data:` lines.
        for line in resp.text.splitlines():
            line = line.strip()
            if line.startswith("data:"):
                payload = line[len("data:"):].strip()
                if payload and payload != "[DONE]":
                    try:
                        return json.loads(payload)
                    except json.JSONDecodeError:
                        continue
        return None
    try:
        return resp.json()
    except Exception:
        return None


async def deepwiki_ask(repo: str, question: str) -> str | None:
    """Call DeepWiki's MCP ``ask_question`` tool over Streamable HTTP.

    MCP-over-HTTP requires an ``initialize`` handshake to obtain a session id
    before tool calls. We perform that handshake, then issue the tool call with
    the negotiated ``Mcp-Session-Id`` header. All failures are non-fatal.
    """
    headers = {
        "Accept": "application/json, text/event-stream",
        "Content-Type": "application/json",
    }
    try:
        client = await _get_deepwiki_client()
        # 1. initialize handshake
        init_payload = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {"name": "pragma", "version": "1.0"},
            },
        }
        init_resp = await client.post(DEEPWIKI_URL, json=init_payload, headers=headers)
        session_id = init_resp.headers.get("mcp-session-id") or init_resp.headers.get("Mcp-Session-Id")
        call_headers = dict(headers)
        if session_id:
            call_headers["Mcp-Session-Id"] = session_id
            # 2. notify initialized (best-effort)
            try:
                await client.post(
                    DEEPWIKI_URL,
                    json={"jsonrpc": "2.0", "method": "notifications/initialized"},
                    headers=call_headers,
                )
            except Exception:
                pass

        # 3. tools/call ask_question
        call_payload = {
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/call",
            "params": {
                "name": "ask_question",
                "arguments": {"repoName": repo, "question": question},
            },
        }
        resp = await client.post(DEEPWIKI_URL, json=call_payload, headers=call_headers)
        if resp.status_code != 200:
            logger.warning(f"DeepWiki tools/call HTTP {resp.status_code}")
            return None

        data = _parse_mcp_response(resp)
        if not data:
            return None
        result = data.get("result", {})
        if isinstance(result, dict) and "content" in result:
            parts = result["content"]
            return "\n".join(p.get("text", "") for p in parts if p.get("type") == "text") or None
        if isinstance(result, str):
            return result
    except Exception as e:
        logger.warning(f"Failed to reach DeepWiki MCP: {e}")
    return None


def ddg_search(query: str, max_results: int = 5) -> list[str]:
    try:
        results = DDGS().text(query, max_results=max_results)
        return [r["body"] for r in results]
    except Exception:
        return []
