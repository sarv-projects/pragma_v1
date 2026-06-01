import asyncio

from pragma_daemon.cache import L1Cache
from pragma_daemon.methods import RPCMethods


def _methods():
    # check_coverage with output_dir does no LLM calls, so a None client is fine.
    return RPCMethods(deepseek=None, cache=L1Cache(), groq=None)


def test_coverage_detects_missing_file(tmp_path):
    m = _methods()
    spec = {"files": [{"path": "app/main.py", "exports": ["app"]}]}
    # No file written -> should be flagged.
    res = asyncio.run(m.check_coverage(spec, {}, output_dir=str(tmp_path)))
    assert res["passed"] is False
    assert any("MISSING_FILE" in i for i in res["issues"])


def test_coverage_detects_missing_export(tmp_path):
    m = _methods()
    (tmp_path / "app").mkdir()
    (tmp_path / "app" / "main.py").write_text("x = 1\n")  # no 'app' export
    spec = {"files": [{"path": "app/main.py", "exports": ["app"]}]}
    res = asyncio.run(m.check_coverage(spec, {}, output_dir=str(tmp_path)))
    assert res["passed"] is False
    assert any("MISSING_EXPORT" in i for i in res["issues"])


def test_coverage_passes_when_complete(tmp_path):
    m = _methods()
    (tmp_path / "app").mkdir()
    (tmp_path / "app" / "main.py").write_text("app = object()\n")
    spec = {"files": [{"path": "app/main.py", "exports": ["app"]}]}
    res = asyncio.run(m.check_coverage(spec, {}, output_dir=str(tmp_path)))
    assert res["passed"] is True
    assert res["issues"] == []
