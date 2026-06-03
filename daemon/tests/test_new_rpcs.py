"""
Tests for the new RPC methods added in the Pythagora/MetaGPT/Wasp/OpenHands
enhancement: extend_project (impact analyzer), fix_runtime_error, apply_delta.

These tests focus on input validation, error handling, and output structure
rather than expensive LLM calls.
"""

import json
import pytest
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

from pragma_daemon.cache import L1Cache
from pragma_daemon.methods import RPCMethods


class DummyResponse:
    def __init__(self, content: str, usage=None):
        self.content = content
        self.usage = usage or MagicMock(input_tokens=10, output_tokens=20)


class DummyGroqResponse:
    def __init__(self, content: str):
        self.content = content
        self.input_tokens = 10
        self.output_tokens = 20


def _make_methods():
    ds = MagicMock()
    ds.chat = AsyncMock()
    return RPCMethods(deepseek=ds, cache=L1Cache(), groq=None)


@pytest.fixture
def methods():
    return _make_methods()


# ─── extend_project impact analyzer ───


@pytest.mark.asyncio
async def test_extend_project_includes_impact_analysis(methods):
    methods.ds.chat.side_effect = [
        DummyResponse(json.dumps({
            "impact_summary": "Add a dark mode toggle to the settings",
            "affected_files": ["frontend/src/Settings.tsx"],
            "new_files": ["frontend/src/DarkModeToggle.tsx"],
            "risk_level": "low",
            "risk_reasons": ["CSS may need refactoring for theme variables"],
        })),
        DummyResponse(json.dumps({
            "files": [
                {"path": "frontend/src/DarkModeToggle.tsx", "role": "component"}
            ],
            "dependencies": ["react-dark-mode-toggle@3.5.0"],
        })),
    ]

    result = await methods.extend_project(
        checkpoint_manifest={"description": "A todo app"},
        checkpoint_spec={"files": [{"path": "frontend/src/App.tsx"}]},
        new_requirements="Add a dark mode toggle",
    )

    assert "impact" in result
    assert "delta" in result
    assert result["impact"]["impact_summary"] == "Add a dark mode toggle to the settings"
    assert result["impact"]["risk_level"] == "low"
    assert len(result["impact"]["affected_files"]) == 1
    assert len(result["impact"]["new_files"]) == 1
    assert len(result["delta"]["files"]) == 1


@pytest.mark.asyncio
async def test_extend_project_handles_impact_analysis_failure_gracefully(methods):
    methods.ds.chat.side_effect = [
        Exception("DeepSeek overloaded"),
        DummyResponse(json.dumps({
            "files": [{"path": "backend/api.py", "role": "route"}],
        })),
    ]

    result = await methods.extend_project(
        checkpoint_manifest={"description": "API backend"},
        checkpoint_spec={"files": []},
        new_requirements="Add rate limiting",
    )

    assert "impact" in result
    assert result["impact"]["risk_level"] == "unknown"
    assert result["impact"]["impact_summary"] == "Pending analysis..."
    assert "delta" in result
    assert len(result["delta"]["files"]) == 1


@pytest.mark.asyncio
async def test_extend_project_handles_delta_failure(methods):
    methods.ds.chat.side_effect = [
        DummyResponse(json.dumps({"impact_summary": "test", "risk_level": "low"})),
        Exception("LLM unavailable"),
    ]

    result = await methods.extend_project(
        checkpoint_manifest={"description": "test"},
        checkpoint_spec={"files": []},
        new_requirements="test",
    )

    assert result["delta"]["error"] == "LLM unavailable"


# ─── fix_runtime_error ───


@pytest.mark.asyncio
async def test_fix_runtime_error_strips_fences(methods):
    methods.ds.chat.return_value = DummyResponse(
        "```python\nimport os\ndef fixed():\n    pass\n```"
    )

    result = await methods.fix_runtime_error(
        error_logs="NameError: name 'os' is not defined",
        file_contract={"path": "app/main.py", "exports": ["fixed"]},
        current_content="def broke():\n    os.getenv('X')",
        profile={},
    )

    assert "import os" in result["content"]
    assert "```" not in result["content"]


@pytest.mark.asyncio
async def test_fix_runtime_error_prefers_groq(methods):
    methods.groq = MagicMock()
    methods.groq.heal_code = AsyncMock()
    methods.groq.heal_code.return_value = DummyGroqResponse("fixed content")

    result = await methods.fix_runtime_error(
        error_logs="SyntaxError",
        file_contract={"path": "app/main.py"},
        current_content="bad code",
        profile={},
    )

    assert result["content"] == "fixed content"
    methods.groq.heal_code.assert_called_once()
    methods.ds.chat.assert_not_called()


@pytest.mark.asyncio
async def test_fix_runtime_error_falls_back_to_deepseek(methods):
    methods.ds.chat.return_value = DummyResponse("fallback fix")

    result = await methods.fix_runtime_error(
        error_logs="Error",
        file_contract={"path": "app/routes.py"},
        current_content="bad",
        profile={},
    )

    assert result["content"] == "fallback fix"


# ─── apply_delta ───


@pytest.mark.asyncio
async def test_apply_delta_creates_new_files(tmp_path, methods):
    methods.ds.chat.return_value = DummyResponse("new content here", usage=MagicMock(input_tokens=5, output_tokens=10))

    output_dir = str(tmp_path)
    delta_spec = {
        "files": [
            {"path": "app/new_file.py", "role": "service", "exports": []}
        ]
    }

    result = await methods.apply_delta(
        run_id="test-123",
        output_dir=output_dir,
        delta_spec=delta_spec,
    )

    assert (tmp_path / "app" / "new_file.py").exists()
    assert "new_file.py" in result["added"]


@pytest.mark.asyncio
async def test_apply_delta_blocks_path_traversal(tmp_path, methods):
    delta_spec = {
        "files": [
            {"path": "../../../etc/passwd", "role": "config"}
        ]
    }

    result = await methods.apply_delta(
        run_id="test-456",
        output_dir=str(tmp_path),
        delta_spec=delta_spec,
    )

    assert len(result["errors"]) >= 1
    assert "Blocked path traversal" in result["errors"][0]
    assert len(result["added"]) == 0


@pytest.mark.asyncio
async def test_apply_delta_merges_existing_file(tmp_path, methods):
    methods.ds.chat.return_value = DummyResponse(
        "def hello():\n    return 'world'\n\ndef goodbye():\n    return 'farewell'"
    )
    (tmp_path / "app").mkdir(parents=True)
    existing = tmp_path / "app" / "existing.py"
    existing.write_text("def hello():\n    return 'world'")

    delta_spec = {
        "files": [
            {
                "path": "app/existing.py",
                "role": "service",
                "public_api": [{"name": "goodbye", "signature": "def goodbye() -> str"}],
            }
        ]
    }

    result = await methods.apply_delta(
        run_id="test-789",
        output_dir=str(tmp_path),
        delta_spec=delta_spec,
    )

    assert "existing.py" in result["modified"]
    content = existing.read_text()
    assert "hello" in content
    assert "goodbye" in content
