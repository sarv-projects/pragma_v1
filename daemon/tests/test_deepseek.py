"""Unit tests for the DeepSeek client's retry, thinking-detection, and
auth-error logic. No network calls — everything is exercised with fakes."""

import httpx

from pragma_daemon.deepseek import DeepSeekClient, _is_retryable_error


def _http_error(status: int) -> httpx.HTTPStatusError:
    req = httpx.Request("GET", "https://example.com")
    resp = httpx.Response(status, request=req)
    return httpx.HTTPStatusError("boom", request=req, response=resp)


def test_429_is_retryable():
    # Rate limits (429) must be retried with backoff.
    assert _is_retryable_error(_http_error(429)) is True


def test_5xx_is_retryable():
    assert _is_retryable_error(_http_error(503)) is True


def test_4xx_not_retryable():
    # Client errors (other than 429) are not retried — a retry won't fix them.
    assert _is_retryable_error(_http_error(400)) is False


def test_timeout_is_retryable():
    assert _is_retryable_error(httpx.TimeoutException("t")) is True


def test_model_supports_thinking():
    c = DeepSeekClient(api_key="x")
    assert c._model_supports_thinking("deepseek-v4-flash") is True
    assert c._model_supports_thinking("deepseek-r1") is True
    assert c._model_supports_thinking("meta/llama-3.1-8b-instruct") is False


class _FakeResp:
    def __init__(self, payload=None, text=""):
        self._payload = payload
        self.text = text

    def json(self):
        if self._payload is None:
            raise ValueError("no json")
        return self._payload


def test_auth_error_message_points_at_deepseek():
    c = DeepSeekClient(api_key="x")
    msg = c._auth_error_message(401, _FakeResp(text="nope"))
    assert "DEEPSEEK_API_KEY" in msg
    assert "401" in msg


def test_auth_error_message_includes_detail():
    c = DeepSeekClient(api_key="x")
    msg = c._auth_error_message(403, _FakeResp({"detail": "Authorization failed"}))
    assert "403" in msg
    assert "platform.deepseek.com" in msg
