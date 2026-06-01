import pytest

from pragma_daemon.spec_compiler import _parse_spec_json, _should_chain


def test_parse_json_fence():
    raw = "Sure!\n```json\n{\"files\": []}\n```"
    assert _parse_spec_json(raw) == {"files": []}


def test_parse_plain_fence():
    raw = "```\n{\"a\": 1}\n```"
    assert _parse_spec_json(raw) == {"a": 1}


def test_parse_bare_json_with_prose():
    raw = 'Here is your spec: {"files": [{"path": "x.py"}]} done.'
    assert _parse_spec_json(raw) == {"files": [{"path": "x.py"}]}


def test_parse_raw_json():
    assert _parse_spec_json('{"k": "v"}') == {"k": "v"}


def test_parse_invalid_raises():
    with pytest.raises(ValueError):
        _parse_spec_json("no json here at all")


def test_should_chain_small_project():
    assert _should_chain({"endpoints": [1, 2], "data_models": [1]}) is False


def test_should_chain_large_project():
    manifest = {"endpoints": list(range(15)), "data_models": list(range(10))}
    assert _should_chain(manifest) is True
