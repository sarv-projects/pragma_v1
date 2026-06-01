"""Integration tests for the RPC method layer.

These exercise the exact JSON shapes the Go orchestrator sends (note the
lowercase profile keys — meta/patterns/conformance_rules — which the B2 fix
guarantees), using a mocked LLM client so no API key or network is needed.
"""

import asyncio
import json

from pragma_daemon.cache import L1Cache
from pragma_daemon.deepseek import ChatResponse, Usage
from pragma_daemon.methods import RPCMethods


# Profile dict exactly as Go now marshals it (lowercase keys, nested context).
GO_PROFILE = {
    "meta": {"name": "FastAPI Async", "language": "python", "version": "3.12"},
    "framework": {"name": "fastapi"},
    "patterns": {"context": "Use async def for all route handlers."},
    "engineering": {"context": "Type hints everywhere."},
    "security": {"context": "bcrypt for passwords."},
    "conformance_rules": {"require_async_handlers": True},
}


class FakeClient:
    """Stands in for DeepSeekClient. Returns queued contents in order."""

    def __init__(self, contents):
        self._contents = list(contents)
        self.calls = 0

    async def chat(self, messages=None, thinking=False, reasoning_effort="high",
                   max_tokens=16384, temperature=0.6):
        self.calls += 1
        content = self._contents.pop(0) if self._contents else self._contents_default()
        return ChatResponse(
            content=content,
            reasoning_content=None,
            usage=Usage(input_tokens=100, output_tokens=50, cached_input_tokens=0),
            model="fake",
        )

    def _contents_default(self):
        return "def placeholder():\n    return None\n"


def test_interview_chat_question_and_complete():
    # First turn: a question. Second turn: scoping complete with manifest.
    client = FakeClient([
        "What database do you want?",
        "[SCOPING_COMPLETE]\n{\"description\": \"task api\", \"endpoints\": []}",
    ])
    m = RPCMethods(deepseek=client, cache=L1Cache(), groq=None)

    r1 = asyncio.run(m.interview_chat(messages=[{"role": "user", "content": "build a task api"}]))
    assert r1["done"] is False
    assert "database" in r1["content"].lower()

    r2 = asyncio.run(m.interview_chat(messages=[{"role": "user", "content": "postgres"}]))
    assert r2["done"] is True
    assert r2["manifest"]["description"] == "task api"


def test_interview_chat_is_cached():
    client = FakeClient(["only question"])
    m = RPCMethods(deepseek=client, cache=L1Cache(), groq=None)
    msgs = [{"role": "user", "content": "hello"}]
    asyncio.run(m.interview_chat(messages=msgs))
    asyncio.run(m.interview_chat(messages=msgs))  # served from cache
    assert client.calls == 1  # A1: cache.get/set work


def test_generate_file_reads_lowercase_profile_and_strips_fences():
    code = "```python\nasync def list_users():\n    return []\n```"
    client = FakeClient([code])
    m = RPCMethods(deepseek=client, cache=L1Cache(), groq=None)
    contract = {"path": "app/routes/users.py", "public_api": [{"name": "list_users"}]}
    res = asyncio.run(m.generate_file(
        file_contract=contract, profile=GO_PROFILE, deps={}, spec_summary="{}",
    ))
    assert "async def list_users" in res["content"]
    assert "```" not in res["content"]
    assert res["healed"] is False  # valid python -> no conformance violations


def test_compile_spec_valid():
    spec = {
        "project_name": "task-api",
        "language": "python",
        "files": [
            {"path": "app/main.py", "role": "config", "exports": ["app"], "depends_on": []},
        ],
        "tests": [{"path": "tests/test_main.py", "cases": []}],
        "dependencies": ["fastapi>=0.115"],
    }
    spec_json = json.dumps(spec)
    # 3 passes all return the same valid spec JSON.
    client = FakeClient([spec_json, spec_json, spec_json])
    m = RPCMethods(deepseek=client, cache=L1Cache(), groq=None)
    manifest = {"description": "task api", "endpoints": [], "data_models": []}
    out = asyncio.run(m.compile_spec(manifest=manifest, research={"findings": []}, profile=GO_PROFILE))
    assert out["project_name"] == "task-api"
    assert client.calls == 3  # draft + optimize + finalize


def test_generate_readme_returns_dict():
    client = FakeClient(["# My Project\nRun it."])
    m = RPCMethods(deepseek=client, cache=L1Cache(), groq=None)
    res = asyncio.run(m.generate_readme(spec={"project_name": "x", "setup": {}}))
    # G6: readme must be a dict with 'content', not a bare string.
    assert isinstance(res, dict)
    assert res["content"].startswith("# My Project")
