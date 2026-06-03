import json
import logging
import re
from typing import Any

from pragma_daemon.deepseek import DeepSeekClient
from pragma_daemon.spec_validator import validate_spec as _validate

logger = logging.getLogger(__name__)

# A spec pass can emit a large file manifest. 16K truncates real projects
# mid-JSON, so give the reasoning passes a much larger budget.
SPEC_MAX_TOKENS = 32000

# Heuristic threshold (entities/endpoints) above which we split the spec into
# domain modules and chain them (§8.7).
CHAIN_THRESHOLD = 18


SPEC_SCHEMA = """
OUTPUT SCHEMA — emit a single JSON object matching this shape EXACTLY:

{
  "project_name": "string (kebab-case)",
  "description": "string",
  "language": "python | typescript | go",
  "dependencies": ["package>=version", "..."],     // external packages only
  "setup": { "run": "string", "test": "string" },
  "files": [
    {
      "path": "relative/path/to/file.ext",
      "role": "short role label (e.g. route, model, service, config, infra, test)",
      "depends_on": ["other/file/path.ext"],         // LOCAL file paths only
      "exports": ["ExportedName", "..."],             // strings; [] for config/infra
      "public_api": [
        {
          "name": "function_or_class_name",
          "signature": "def name(args) -> ReturnType",
          "args": ["arg: type"],
          "returns": "type",
          "description": "what it does"
        }
      ]
    }
  ],
  "tests": [
    { "path": "tests/test_x.py", "cases": [{ "name": "test_case_name", "asserts": "what it verifies" }] }
  ],
    "deployment": { "dockerfile": true, "compose": true, "ci": true }
}

RULES:
- Every code file MUST declare exports OR public_api (config/infra files may have empty exports).
- depends_on lists LOCAL file paths from this same manifest only — never external packages.
- No dependency cycles. Order files so dependencies appear before dependents where possible.
- Every endpoint in the requirements maps to a route file with a matching handler in public_api.
- Every data model maps to a model file.
- The spec MUST always include a Dockerfile and docker-compose.yml in the files list regardless of project type. Add them with role "infra" and appropriate depends_on.
- Include a .github/workflows/ci.yml file with the project language's standard CI pipeline (lint + test + build).
- Include an OpenAPI 3.0 specification file (openapi.yaml) with role "openapi" that documents all REST API endpoints, request/response schemas, parameters, and authentication methods. Generate this even for simple projects.
- Include a .github/workflows/ci.yml file with role "infra" that sets up CI/CD pipeline with: linting, testing, and Docker image building (if deployment.dockerfile=true). For Python: use ruff + pytest. For TypeScript: use eslint + vitest. For Go: use golangci-lint + go test. Trigger on push to main and PRs.
- Include a .github/workflows/deploy.yml file with role "infra" for deployment automation if the project has deployment configs. Use GitHub Actions for common platforms (Vercel, Fly.io, Railway, etc.) with environment secrets.
- Output ONLY the JSON object. No prose, no markdown fences around prose.
"""


class SpecCompilationError(Exception):
    pass


async def compile_spec(
    client: DeepSeekClient,
    manifest: dict,
    research_context: dict,
    profile: dict,
    complexity: str = "simple",
) -> dict:
    if _should_chain(manifest):
        logger.info("Large project detected -- compiling spec in chained domain modules")
        try:
            return await _compile_chained(client, manifest, research_context, profile, complexity=complexity)
        except Exception as e:
            logger.warning(f"Chained compilation failed ({e}); falling back to single 3-pass compile")

    return await _compile_single(client, manifest, research_context, profile, complexity=complexity)


async def _compile_single(
    client: DeepSeekClient,
    manifest: dict,
    research_context: dict,
    profile: dict,
    complexity: str = "simple",
) -> dict:
    system_prompt = _build_system_prompt(profile, complexity=complexity, manifest=manifest)

    # Pass 1: Draft — the only pass that needs reasoning.
    # thinking=True enables chain-of-thought. The client automatically selects
    # the best available reasoning model (V4 Pro if in cache, Flash otherwise).
    # Model upgrade is automatic — no code change needed when Pro becomes available.
    pass1_messages = _build_pass1_messages(system_prompt, manifest, research_context)
    logger.info("Starting Pass 1 (Draft, thinking=medium)")
    pass1_output = await _pass(client, pass1_messages, thinking=True, effort="medium")

    # Pass 2: Optimize -- a refinement of an existing draft; no reasoning needed.
    pass2_messages = _build_pass2_messages(system_prompt, manifest, research_context, pass1_output)
    logger.info("Starting Pass 2 (Optimize)")
    pass2_output = await _pass(client, pass2_messages, thinking=False, effort="low")

    # Pass 3 is conditional: only run if validation finds issues after Pass 2.
    try:
        pass2_spec = _parse_spec_json(pass2_output)
        pass2_errors = _validate(pass2_spec, manifest)
        pass2_fatal = [e for e in pass2_errors if e.severity == "error"]
        if not pass2_fatal:
            logger.info("Pass 2 output validates cleanly; skipping Pass 3")
            return pass2_spec
    except Exception:
        # If Pass 2 output cannot be parsed, proceed with Pass 3
        pass

    # Pass 3: Finalize -- cleanup only.
    pass3_messages = _build_pass3_messages(system_prompt, pass2_output)
    logger.info("Starting Pass 3 (Finalize)")
    pass3_output = await _pass(client, pass3_messages, thinking=False, effort="low")

    try:
        return _parse_spec_json(pass3_output)
    except Exception as e:
        logger.error(f"Failed to parse spec JSON: {e}")
        raise SpecCompilationError("Failed to produce valid spec.json") from e


def _should_chain(manifest: dict) -> bool:
    endpoints = len(manifest.get("endpoints", []) or [])
    models = len(manifest.get("data_models", []) or [])
    return (endpoints + models) > CHAIN_THRESHOLD


# Domain modules compiled in order; each receives the merged output of all
# prior modules as context (§8.7).
_CHAIN_MODULES = [
    ("core", "configuration, database setup, security/auth primitives, shared utilities"),
    ("models", "data entities and their relationships"),
    ("services", "business logic / service layer operating on the models"),
    ("routes", "API surface — route handlers wiring services to endpoints"),
    ("tests", "unit and integration tests covering the above"),
]


async def _compile_chained(
    client: DeepSeekClient,
    manifest: dict,
    research_context: dict,
    profile: dict,
    complexity: str = "simple",
) -> dict:
    system_prompt = _build_system_prompt(profile, complexity=complexity, manifest=manifest)
    merged: dict[str, Any] = {
        "project_name": manifest.get("project_name", "project"),
        "description": manifest.get("description", ""),
        "language": profile.get("meta", {}).get("language", "python"),
        "dependencies": [],
        "files": [],
        "tests": [],
    }
    seen_paths: set[str] = set()

    for module_name, module_desc in _CHAIN_MODULES:
        context_summary = _summarise_for_context(merged)
        messages = [
            {"role": "system", "content": system_prompt + "\n" + SPEC_SCHEMA},
            {
                "role": "user",
                "content": (
                    f"Research context:\n{json.dumps(research_context)}\n\n"
                    f"Requirements:\n{json.dumps(manifest)}\n\n"
                    f"Already-specified files (context, do NOT repeat):\n{context_summary}\n\n"
                    f"Now produce the spec.json for the '{module_name}' module ONLY "
                    f"({module_desc}). Use the same schema. depends_on may reference "
                    f"already-specified files. Output ONLY JSON."
                ),
            },
        ]
        logger.info(f"Chained compile — module '{module_name}'")
        out = await _pass(client, messages, thinking=True, effort="medium")
        try:
            module_spec = _parse_spec_json(out)
        except Exception as e:
            logger.warning(f"Module '{module_name}' produced invalid JSON ({e}); skipping")
            continue

        for f in module_spec.get("files", []):
            if isinstance(f, dict) and f.get("path") and f["path"] not in seen_paths:
                seen_paths.add(f["path"])
                merged["files"].append(f)
        for t in module_spec.get("tests", []):
            merged["tests"].append(t)
        for dep in module_spec.get("dependencies", []):
            if dep not in merged["dependencies"]:
                merged["dependencies"].append(dep)
        if module_spec.get("setup"):
            merged.setdefault("setup", module_spec["setup"])
        if module_spec.get("deployment"):
            merged.setdefault("deployment", module_spec["deployment"])

    if not merged["files"]:
        raise SpecCompilationError("Chained compilation produced no files")
    return merged


def _summarise_for_context(merged: dict) -> str:
    lines = []
    for f in merged.get("files", []):
        exports = f.get("exports") or [a.get("name") for a in f.get("public_api", []) if isinstance(a, dict)]
        lines.append(f"- {f.get('path')} (exports: {', '.join(str(e) for e in exports) or 'n/a'})")
    return "\n".join(lines) if lines else "(none yet)"


async def _pass(
    client: DeepSeekClient,
    messages: list[dict],
    thinking: bool,
    effort: str,
) -> str:
    response = await client.chat(
        messages=messages,
        thinking=thinking,
        reasoning_effort=effort,
        max_tokens=SPEC_MAX_TOKENS,
    )
    return response.content


def _is_user_facing(manifest: dict | None) -> bool:
    """Determine if the manifest describes a user-facing application (vs. a pure API).
    
    Checks the description and endpoint paths for UI-related keywords.
    Pure API-only projects (e.g. "REST API for ...", "backend service") are
    not considered user-facing and won't get frontend generation.
    """
    if not manifest:
        return False
    desc = (manifest.get("description") or "").lower()
    endpoints = manifest.get("endpoints", []) or []
    auth = (manifest.get("auth") or "").lower()

    # Pure API indicators — if these dominate, skip frontend
    pure_api_keywords = ["api", "rest api", "backend", "backend service", "microservice", "sdk", "cli"]
    api_score = sum(1 for kw in pure_api_keywords if kw in desc)

    # User-facing indicators
    ui_keywords = [
        "app", "dashboard", "ui", "interface", "user interface", "frontend", "web app",
        "todo", "blog", "ecommerce", "store", "shop", "social", "chat", "messaging",
        "booking", "calendar", "management system", "portal", "crm", "cms",
        "landing page", "website", "page", "form", "login", "signup", "register",
    ]
    ui_score = sum(1 for kw in ui_keywords if kw in desc)

    # If endpoints are all under /api/ and description is pure API, don't generate frontend
    all_api_endpoints = all(
        isinstance(e, dict) and e.get("path", "").startswith("/api/")
        for e in endpoints
    ) if endpoints else False

    # If API terms dominate or equal UI terms in the description, treat as pure API.
    # This handles cases like "A REST API for managing todo items" where the
    # primary subject is the API, not the user-facing todo management.
    if api_score >= ui_score and api_score >= 1:
        return False

    # No auth (anonymous) + pure API description + all /api/ endpoints = backend-only
    if auth in ("none", "", "no", "anonymous") and api_score >= 1 and all_api_endpoints and ui_score == 0:
        return False

    # Explicitly user-facing
    if ui_score >= 1:
        return True

    # Default heuristic: if description mentions building "for users" or has
    # multiple endpoints that suggest a UI (CRUD on user-facing entities)
    user_entity_keywords = ["user", "customer", "patient", "student", "employee", "member"]
    if any(kw in desc for kw in user_entity_keywords) and len(endpoints) >= 3:
        return True

    return False


def _build_system_prompt(profile: dict, complexity: str = "simple", manifest: dict | None = None) -> str:
    patterns = profile.get("patterns", {}).get("context", "")
    engineering = profile.get("engineering", {}).get("context", "")
    security = profile.get("security", {}).get("context", "")
    meta = profile.get("meta", {})
    language = meta.get("language", "python")
    profile_name = meta.get("name", "Unknown")

    prompt = f"""You are Pragma's architectural spec compiler. Your job is to produce a deterministic Build Contract:
a spec so complete that a non-thinking model can implement every file from its contract alone.

Target stack: {profile_name} ({language} {meta.get('version', '')})

Framework patterns:
{patterns}

Engineering standards:
{engineering}

Security standards:
{security}
{SPEC_SCHEMA}
Before outputting, self-verify: no dependency cycles, all local imports resolve,
every endpoint has a handler, every relationship target exists.

ALWAYS include Dockerfile and docker-compose.yml in the files list. Every project must have Docker support."""

    if complexity == "simple":
        prompt += (
            "\n\nCOMPLEXITY LEVEL: Simple. Design a monolithic architecture. "
            "Minimize file count (aim for 8-15 files). "
            "No microservices, no message queues, no caching layers. Keep it straightforward."
        )
    elif complexity == "advanced":
        prompt += "\n\nCOMPLEXITY LEVEL: Advanced. Full production architecture is appropriate."

    if complexity == "simple":
        if language == "python":
            prompt += (
                "\n\nDATABASE OVERRIDE: Use SQLite (not PostgreSQL). "
                "Use aiosqlite + SQLAlchemy with sqlite+aiosqlite:///./data.db as the database URL. "
                "Replace async_sessionmaker with sqlite-compatible settings. Do NOT import asyncpg or psycopg2. "
                "Add aiosqlite to dependencies instead of asyncpg."
            )
        elif language == "typescript":
            prompt += (
                "\n\nDATABASE OVERRIDE: Use SQLite (not PostgreSQL). "
                "Use better-sqlite3 with Drizzle's better-sqlite3 driver (drizzle-orm/better-sqlite3). "
                "Replace postgres-js with better-sqlite3. Do NOT include postgres-js or pg in dependencies. "
                "Update the database connection to use ./data.db as the database file path."
            )
        elif language == "go":
            prompt += (
                "\n\nDATABASE OVERRIDE: Use SQLite (not PostgreSQL). "
                "Use mattn/go-sqlite3 (github.com/mattn/go-sqlite3) as the database driver. "
                "Do NOT include pgx or any PostgreSQL driver in dependencies. "
                "Replace sqlc-generated code with direct sqlite3 queries using database/sql. "
                "The database file should be at ./data.db."
            )

    if language == "typescript":
        prompt += (
            "\n\nOPENAPI: Include an OpenAPI specification for the API. "
            "Add a src/swagger.ts config file that uses swagger-jsdoc to generate an OpenAPI 3.0.0 spec "
            "from JSDoc annotations on route handlers. Use swagger-ui-express to serve the docs at /api-docs. "
            "For Hono profiles, use @hono/swagger-ui middleware and serve the spec at /doc and UI at /ui. "
            "Annotate every route handler with @openapi JSDoc comments including path, method, summary, "
            "tags, requestBody (for POST/PUT/PATCH), and response schemas. "
            "Export the raw OpenAPI JSON at /api-docs.json or /openapi.json."
        )
    elif language == "go":
        prompt += (
            "\n\nOPENAPI: Include an OpenAPI specification for the API. "
            "Add swaggo/swag annotations to all handler functions (@Summary, @Description, @Tags, "
            "@Param, @Success, @Failure, @Router). "
            "Serve the OpenAPI spec at /swagger.json and include a minimal swagger UI route. "
            "Add a docs/ directory with generated swagger docs."
        )

    if _is_user_facing(manifest) and profile_name.lower() != "next.js app router":
        prompt += (
            "\n\nFRONTEND: The project describes a user-facing application. "
            "Generate a complete frontend SPA in a frontend/ directory with these files:\n"
            "- frontend/index.html - HTML entry point\n"
            "- frontend/src/main.tsx - React entry point\n"
            "- frontend/src/App.tsx - Main app component with React Router\n"
            "- frontend/vite.config.ts - Vite configuration with API proxy to the backend\n"
            "- frontend/package.json - Dependencies including react, react-dom, react-router-dom, typescript, vite\n"
            "- frontend/tsconfig.json - TypeScript configuration\n"
            "- frontend/tsconfig.node.json - Node TypeScript config for vite config\n"
            "The React app should provide a clean UI that consumes the API endpoints defined in this spec. "
            "Include pages/components for the main features described in the requirements. "
            "Use a simple, clean CSS approach (no heavy CSS frameworks). "
            "The docker-compose.yml should serve the frontend via a separate nginx or serve it from the backend. "
            "Add all required npm dependencies to the project's dependency list."
        )

    if _is_user_facing(manifest) and profile_name.lower() != "next.js app router" and language == "typescript":
        prompt += (
            "\n\nSHARED TYPES: Both the frontend and backend use TypeScript. "
            "Generate a shared/ directory at the project root with TypeScript type definitions "
            "used across the stack:\n"
            "- shared/types.ts — API request/response interfaces, enums, and type aliases matching the data model\n"
            "- shared/index.ts — barrel export of all shared types\n"
            "Backend files should import types from '../shared/types' or '../shared/index'. "
            "Frontend files should import types from '../shared/types' or '../shared/index'. "
            "Additionally, use openapi-typescript to generate TypeScript types automatically "
            "from the OpenAPI spec. Add openapi-typescript as a dev dependency. "
            "Add a 'typegen' script to package.json: "
            '"openapi-typescript ./openapi.json -o ./shared/api.ts". '
            "This keeps the types in sync with the API spec. "
            "Include both the hand-authored shared/types.ts and the generated shared/api.ts."
        )

    prompt += (
        "\n\nCI/CD: Include a .github/workflows/ci.yml file with the project's CI pipeline. "
        "For Python: use pytest with pip install. "
        "For TypeScript: use npm ci, npm run build, npm test. "
        "For Go: use go build, go vet, go test. "
        "The CI should run on push and pull_request to the main branch. "
        "Include lint, build, and test steps."
    )

    return prompt


def _build_pass1_messages(system: str, manifest: dict, research: dict) -> list[dict]:
    role_prompt = (
        "ROLE: You are the Lead Software Architect. Your goal is to design a robust, scalable, "
        "and complete system structure. Focus on: correct data models, comprehensive API routes, "
        "proper dependency injection, and a logical file structure. Do not worry about minor "
        "formatting yet; focus on architectural completeness."
    )
    return [
        {"role": "system", "content": system + "\n\n" + role_prompt},
        {
            "role": "user",
            "content": f"Research context:\n{json.dumps(research)}\n\nRequirements:\n{json.dumps(manifest)}\n\nDraft the complete spec.json.",
        },
    ]


def _build_pass2_messages(system: str, manifest: dict, research: dict, pass1_output: str) -> list[dict]:
    role_prompt = (
        "ROLE: You are the Security & Database Expert. Your goal is to critically review the "
        "Architect's draft. Focus exclusively on: missing authentication/authorization checks, "
        "SQL injection risks, missing database indexes, improper error handling, and ensuring "
        "all local imports resolve. Fix any architectural gaps you find."
    )
    return [
        {"role": "system", "content": system + "\n\n" + role_prompt},
        {"role": "user", "content": f"Research context:\n{json.dumps(research)}\n\nRequirements:\n{json.dumps(manifest)}"},
        {"role": "assistant", "content": pass1_output},
        {
            "role": "user",
            "content": "Review the draft. Fix dependency cycles, missing error handling, security gaps, "
            "and ensure all interfaces match the schema. Provide the complete updated JSON.",
        },
    ]


def _build_pass3_messages(system: str, pass2_output: str) -> list[dict]:
    role_prompt = (
        "ROLE: You are the Finalizer. Your goal is to ensure the spec is perfectly formatted, "
        "consistent, and ready for code generation. Focus on: consistent naming conventions, "
        "valid JSON structure, resolving all 'depends_on' links, and removing any redundancy. "
        "Output ONLY the final, valid JSON object."
    )
    return [
        {"role": "system", "content": system + "\n\n" + role_prompt},
        {
            "role": "user",
            "content": f"Clean and finalize this spec JSON (consistent naming, valid structure, no redundancy). "
            f"Output ONLY valid JSON:\n{pass2_output}",
        },
    ]


def _parse_spec_json(raw: str) -> dict:
    """Extract a JSON object from a model response.

    Handles: ```json fences, plain ``` fences, and bare JSON with optional
    prose before/after the object.
    """
    if not raw:
        raise ValueError("empty spec output")

    text = raw.strip()

    # 1. Fenced block (```json ... ``` or ``` ... ```).
    fence = re.search(r"```(?:json)?\s*(.*?)```", text, re.DOTALL)
    if fence:
        candidate = fence.group(1).strip()
        try:
            return json.loads(candidate)
        except json.JSONDecodeError:
            text = candidate  # fall through to brace extraction

    # 2. Direct parse.
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass

    # 3. Brace extraction — robust counting to ignore trailing garbage
    start = text.find("{")
    if start >= 0:
        count = 0
        for i in range(start, len(text)):
            if text[i] == '{':
                count += 1
            elif text[i] == '}':
                count -= 1
                if count == 0:
                    try:
                        return json.loads(text[start:i+1])
                    except json.JSONDecodeError:
                        break

    raise ValueError("no JSON object found in spec output")
