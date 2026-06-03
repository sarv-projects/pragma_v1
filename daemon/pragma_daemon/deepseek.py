import json as _json
import logging
import time
from dataclasses import dataclass
from pathlib import Path

import httpx
from tenacity import retry, retry_if_exception, stop_after_attempt, wait_exponential, before_sleep_log

logger = logging.getLogger(__name__)


class CreditsExhaustedError(Exception):
    """Raised when the API returns HTTP 402 — account credits are depleted."""


def _is_retryable_error(e: Exception) -> bool:
    """Retry on timeouts, rate limits (429), and transient server errors (5xx)."""
    if isinstance(e, httpx.TimeoutException):
        return True
    if isinstance(e, httpx.HTTPStatusError):
        code = e.response.status_code
        return code == 429 or code >= 500
    return False


@dataclass
class Usage:
    input_tokens: int
    output_tokens: int
    cached_input_tokens: int


@dataclass
class ChatResponse:
    content: str
    reasoning_content: str | None
    usage: Usage
    model: str


class DeepSeekClient:
    """Async client for the DeepSeek chat-completions API.

    DeepSeek V4-family models support reasoning ("thinking") mode, which is
    enabled per-call via the ``thinking`` flag — used for spec compilation
    Pass 1 and disabled for fast codegen. Models are discovered at startup and
    cached for 24h (see ensure_model_cache).
    """

    def __init__(self, api_key: str, base_url: str = "https://api.deepseek.com"):
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        # Reasoning ("thinking") passes can stream for several minutes. A short
        # global timeout used to fire at 120s, get classified as retryable, and
        # trigger up to 4 full re-generations — turning one slow pass into ~8
        # minutes of dead retries and tripping the orchestrator's deadline.
        # Give reads a generous budget; connect/write stay short.
        self.client = httpx.AsyncClient(
            timeout=httpx.Timeout(connect=15.0, read=300.0, write=60.0, pool=300.0)
        )
        self._available_models: list[str] = []  # populated by ensure_model_cache()
        self._reasoning_model: str = ""
        self._codegen_model: str = ""
        self._chat_model: str = ""   # small/fast model for conversational turns (interview)
        self._fallback_model: str = ""

    async def close(self):
        """Close the underlying HTTP client to release resources."""
        if self.client:
            await self.client.aclose()

    async def discover_models(self) -> list[str]:
        url = f"{self.base_url}/models"
        headers = {"Authorization": f"Bearer {self.api_key}"}
        try:
            response = await self.client.get(url, headers=headers, timeout=10.0)
            if response.status_code != 200:
                raise ValueError(f"HTTP {response.status_code}: {response.text}")
            data = response.json()
            models = [m["id"] for m in data.get("data", [])]
            logger.info(f"Discovered {len(models)} models from DeepSeek")
            return models
        except Exception as e:
            logger.warning(f"Model discovery failed: {e}. Using hardcoded fallback.")
            # Hardcoded fallback — the models we know exist as of May 2026
            return ["deepseek-v4-flash"]

    def _model_supports_thinking(self, model_id: str) -> bool:
        """Heuristic: DeepSeek V4/R1 families and known reasoners support thinking."""
        mid = (model_id or "").lower()
        thinking_markers = ("deepseek-v4", "deepseek-r1", "deepseek-reasoner", "qwen3", "nemotron-ultra")
        return any(marker in mid for marker in thinking_markers)

    def _auth_error_message(self, status: int, response: "httpx.Response") -> str:
        """Build an actionable message for a 401/403 from the provider."""
        detail = ""
        try:
            body = response.json()
            if isinstance(body, dict):
                detail = body.get("detail") or body.get("title") or body.get("message") or ""
        except Exception:
            detail = (response.text or "")[:160]

        return (
            f"DeepSeek rejected the request (HTTP {status}: {detail or 'Authorization failed'}). "
            "Check that DEEPSEEK_API_KEY is set and valid (regenerate at https://platform.deepseek.com)."
        )

    async def ensure_model_cache(self) -> list[str]:
        """Discover available DeepSeek models and pick reasoning/codegen/fallback.

        Results are cached in ~/.pragma/models.json for 24h so startup is fast
        and resumed runs don't re-probe. DeepSeek currently exposes a small set
        of models, so first-available is a fine default.
        """
        cache_path = Path.home() / ".pragma" / "models.json"

        if cache_path.exists():
            try:
                data = _json.loads(cache_path.read_text())
                if isinstance(data, dict):
                    cache_age = time.time() - data.get("ts", 0)
                    if cache_age < 86400 and data.get("reasoning_model"):
                        self._reasoning_model = data["reasoning_model"]
                        self._codegen_model = data["codegen_model"]
                        self._fallback_model = data["fallback_model"]
                        self._available_models = data.get("available_models", [])
                        logger.info(
                            f"Loaded model cache ({cache_age / 3600:.1f}h old): "
                            f"reasoning={self._reasoning_model}, codegen={self._codegen_model}"
                        )
                        return self._available_models
            except Exception:
                pass

        available = await self.discover_models()
        self._available_models = available

        self._reasoning_model = available[0] if available else "deepseek-v4-flash"
        self._codegen_model = available[0] if available else "deepseek-v4-flash"
        self._fallback_model = available[-1] if available else "deepseek-v4-flash"

        cache_data = {
            "ts": time.time(),
            "available_models": available,
            "reasoning_model": self._reasoning_model,
            "codegen_model": self._codegen_model,
            "fallback_model": self._fallback_model,
        }
        cache_path.parent.mkdir(parents=True, exist_ok=True)
        import os
        cache_tmp = cache_path.with_suffix(".tmp")
        cache_tmp.write_text(_json.dumps(cache_data, indent=2))
        os.replace(cache_tmp, cache_path)
        logger.info(
            f"Model cache written: reasoning={self._reasoning_model}, codegen={self._codegen_model}"
        )
        return available

    @retry(
        wait=wait_exponential(multiplier=1, min=2, max=20, max_jitter=5),
        stop=stop_after_attempt(4),
        retry=retry_if_exception(_is_retryable_error),
        before_sleep=before_sleep_log(logger, logging.WARNING),
    )
    async def chat(
        self,
        messages: list[dict],
        thinking: bool = False,
        reasoning_effort: str = "high",
        max_tokens: int = 16384,
        temperature: float = 0.6,
    ) -> ChatResponse:
        # Pick model based on task type: reasoning_model for thinking tasks,
        # codegen_model for everything else.
        if thinking and self._reasoning_model:
            model_id = self._reasoning_model
        elif not thinking and self._codegen_model:
            model_id = self._codegen_model
        elif self._available_models:
            model_id = self._available_models[0]
        else:
            model_id = "deepseek-v4-flash"

        url = f"{self.base_url}/chat/completions"
        headers = {
            "Authorization": f"Bearer {self.api_key}",
            "Content-Type": "application/json",
        }

        payload = {
            "model": model_id,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
        }

        # Enable reasoning only when the task asks for it AND the chosen model
        # supports it. DeepSeek silently ignores unknown params on some models,
        # so we also guard against 400/422 below.
        send_thinking = thinking and self._model_supports_thinking(model_id)
        if send_thinking:
            payload["thinking"] = {"type": "enabled"}
            payload["reasoning_effort"] = reasoning_effort

        response = await self.client.post(url, headers=headers, json=payload)

        # Some providers reject unknown params with 400/422 — retry once without
        # the thinking fields rather than failing the whole pass.
        if send_thinking and response.status_code in (400, 422):
            logger.warning(
                f"Model {model_id} rejected thinking params (HTTP {response.status_code}); retrying without."
            )
            payload.pop("thinking", None)
            payload.pop("reasoning_effort", None)
            response = await self.client.post(url, headers=headers, json=payload)

        # On 404 model not found — re-discover and fall back
        if response.status_code == 404:
            logger.warning(f"Model {model_id} returned 404. Re-discovering and using fallback...")
            self._available_models = await self.discover_models()
            fallback = self._fallback_model or (self._available_models[0] if self._available_models else None)
            if fallback and fallback != model_id:
                # Update instance variables so subsequent requests use the working model
                if thinking and self._reasoning_model == model_id:
                    self._reasoning_model = fallback
                elif not thinking and self._codegen_model == model_id:
                    self._codegen_model = fallback
                
                payload["model"] = fallback
                if not self._model_supports_thinking(fallback):
                    payload.pop("thinking", None)
                    payload.pop("reasoning_effort", None)
                response = await self.client.post(url, headers=headers, json=payload)
                # Also invalidate cache so next startup re-probes
                cache_path = Path.home() / ".pragma" / "models.json"
                if cache_path.exists():
                    cache_path.unlink()

        # 402: account credits exhausted. Not retryable — user must top up.
        if response.status_code == 402:
            raise CreditsExhaustedError(
                "API credits exhausted (HTTP 402). "
                "Top up your account at https://platform.deepseek.com — "
                "your checkpoint is saved, restart Pragma to resume."
            )

        # 401/403: authentication / authorization problem. These are NOT model
        # issues (a different model won't help) and NOT retryable, so fail fast
        # with actionable guidance instead of a raw httpx error + MDN link.
        if response.status_code in (401, 403):
            raise RuntimeError(self._auth_error_message(response.status_code, response))

        response.raise_for_status()

        data = response.json()
        choice = data["choices"][0]["message"]
        usage = data.get("usage", {})

        return ChatResponse(
            content=choice.get("content", "") or "",
            reasoning_content=choice.get("reasoning_content"),
            usage=Usage(
                input_tokens=usage.get("prompt_tokens", 0),
                output_tokens=usage.get("completion_tokens", 0),
                cached_input_tokens=usage.get("prompt_cache_hit_tokens", 0),
            ),
            model=data.get("model", model_id),
        )
