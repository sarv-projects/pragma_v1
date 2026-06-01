"""Unit tests for the Groq client. No network — _chat is monkeypatched."""

import httpx
import pytest

from pragma_daemon.groq_client import GroqClient, GroqResponse, _is_retryable


def test_429_is_retryable():
    req = httpx.Request("GET", "https://example.com")
    err = httpx.HTTPStatusError("rate", request=req, response=httpx.Response(429, request=req))
    assert _is_retryable(err) is True


def test_4xx_not_retryable():
    req = httpx.Request("GET", "https://example.com")
    err = httpx.HTTPStatusError("bad", request=req, response=httpx.Response(400, request=req))
    assert _is_retryable(err) is False


def test_timeout_is_retryable():
    assert _is_retryable(httpx.TimeoutException("t")) is True


@pytest.mark.asyncio
async def test_chat_interview_routes_to_llama(monkeypatch):
    client = GroqClient(api_key="x")
    captured = {}

    async def fake_chat(model, messages, max_tokens=4096, temperature=0.3, tools=None):
        captured["model"] = model
        return GroqResponse(content="hi", model=model, input_tokens=1, output_tokens=1)

    monkeypatch.setattr(client, "_chat", fake_chat)
    resp = await client.chat([{"role": "user", "content": "hello"}], purpose="interview")
    assert resp.content == "hi"
    assert "llama" in captured["model"]


@pytest.mark.asyncio
async def test_chat_heal_routes_to_heal_code(monkeypatch):
    client = GroqClient(api_key="x")
    captured = {}

    async def fake_chat(model, messages, max_tokens=4096, temperature=0.3, tools=None):
        captured["model"] = model
        return GroqResponse(content="fixed", model=model, input_tokens=1, output_tokens=1)

    monkeypatch.setattr(client, "_chat", fake_chat)
    resp = await client.chat([{"role": "user", "content": "fix"}], purpose="heal")
    assert resp.content == "fixed"
    # heal_code tries gpt-oss-20b first
    assert "gpt-oss" in captured["model"]
