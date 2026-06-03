"""Generic OpenAI-compatible LLM client for BYOK (Bring Your Own Key) support.

Works with any provider that exposes an OpenAI-compatible /chat/completions
endpoint: OpenAI, Anthropic (via proxy), Ollama, OpenRouter, Together,
Fireworks, DeepInfra, Groq, Mistral, etc.

This client is used as the "codegen" provider when the user configures a
custom provider instead of the default DeepSeek.
"""

import json as _json
import logging
import time
from dataclasses import dataclass
from pathlib import Path

import httpx
from tenacity import (
    retry,
    retry_if_exception,
    stop_after_attempt,
    wait_exponential,
    before_sleep_log,
)

logger = logging.getLogger(__name__)


class CreditsExhaustedError(Exception):
    """Raised when the API returns HTTP 402."""


def _is_retryable_error(e: Exception) -> bool:
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


class OpenAICompatClient:
    """Async client for any OpenAI-compatible chat completions API.

    Supports:
    - Standard /chat/completions endpoint
    - Optional thinking/reasoning mode (for models that support it)
    - Automatic model discovery via /models endpoint
    - Retry with exponential backoff
    """

    def __init__(
        self,
        api_key: str,
        base_url: str = "https://api.openai.com/v1",
        provider_name: str = "custom",
        reasoning_model: str = "",
        codegen_model: str = "",
        supports_thinking: bool = False,
    ):
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        self.provider_name = provider_name
        self.supports_thinking = supports_thinking

        self.client = httpx.AsyncClient(
            timeout=httpx.Timeout(connect=15.0, read=300.0, write=60.0, pool=300.0)
        )
        self._available_models: list[str] = []
        self._reasoning_model = reasoning_model
        self._codegen_model = codegen_model
        self._fallback_model = ""

    async def close(self):
        if self.client:
            await self.client.aclose()

    async def discover_models(self) -> list[str]:
        url = f"{self.base_url}/models"
        headers = {"Authorization": f"Bearer {self.api_key}"}
        try:
            response = await self.client.get(url, headers=headers, timeout=10.0)
            if response.status_code != 200:
                logger.warning(f"Model discovery returned HTTP {response.status_code}")
                return []
            data = response.json()
            models = [m["id"] for m in data.get("data", [])]
            logger.info(f"[{self.provider_name}] Discovered {len(models)} models")
            return models
        except Exception as e:
            logger.warning(f"[{self.provider_name}] Model discovery failed: {e}")
            return []

    async def ensure_model_cache(self) -> list[str]:
        """Discover models and pick reasoning/codegen models.

        If the user specified explicit model names in config, those take
        precedence. Otherwise we auto-discover and pick the first available.
        """
        # If user provided explicit model names, use those
        if self._reasoning_model and self._codegen_model:
            logger.info(
                f"[{self.provider_name}] Using configured models: "
                f"reasoning={self._reasoning_model}, codegen={self._codegen_model}"
            )
            return [self._reasoning_model, self._codegen_model]

        cache_path = Path.home() / ".pragma" / f"models_{self.provider_name}.json"

        # Try cache first
        if cache_path.exists():
            try:
                data = _json.loads(cache_path.read_text())
                if isinstance(data, dict):
                    cache_age = time.time() - data.get("ts", 0)
                    if cache_age < 86400 and data.get("codegen_model"):
                        self._reasoning_model = data.get("reasoning_model", "")
                        self._codegen_model = data["codegen_model"]
                        self._fallback_model = data.get("fallback_model", "")
                        self._available_models = data.get("available_models", [])
                        logger.info(
                            f"[{self.provider_name}] Loaded model cache ({cache_age / 3600:.1f}h old)"
                        )
                        return self._available_models
            except Exception:
                pass

        # Discover from API
        available = await self.discover_models()
        self._available_models = available

        if not available:
            # No models discovered — user must configure explicit model names
            logger.warning(
                f"[{self.provider_name}] No models discovered. "
                "Configure explicit model names in ~/.pragma/config.toml"
            )
            return []

        # Pick models: prefer names containing common patterns
        reasoning_candidates = [
            m for m in available
            if any(k in m.lower() for k in ["reason", "think", "r1", "o1", "o3", "qwen3"])
        ]
        codegen_candidates = [
            m for m in available
            if any(k in m.lower() for k in ["code", "coder", "instruct", "chat"])
        ]

        self._reasoning_model = (
            self._reasoning_model
            or (reasoning_candidates[0] if reasoning_candidates else available[0])
        )
        self._codegen_model = (
            self._codegen_model
            or (codegen_candidates[0] if codegen_candidates else available[0])
        )
        self._fallback_model = available[-1] if len(available) > 1 else available[0]

        # Cache results
        cache_data = {
            "ts": time.time(),
            "available_models": available,
            "reasoning_model": self._reasoning_model,
            "codegen_model": self._codegen_model,
            "fallback_model": self._fallback_model,
        }
        try:
            cache_path.parent.mkdir(parents=True, exist_ok=True)
            cache_tmp = cache_path.with_suffix(".tmp")
            cache_tmp.write_text(_json.dumps(cache_data, indent=2))
            import os
            os.replace(cache_tmp, cache_path)
        except Exception as e:
            logger.warning(f"[{self.provider_name}] Failed to write model cache: {e}")

        logger.info(
            f"[{self.provider_name}] Models: reasoning={self._reasoning_model}, "
            f"codegen={self._codegen_model}"
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
        if thinking and self._reasoning_model:
            model_id = self._reasoning_model
        elif not thinking and self._codegen_model:
            model_id = self._codegen_model
        elif self._available_models:
            model_id = self._available_models[0]
        else:
            model_id = self._codegen_model or "default"

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

        # Only send thinking params if the provider/model supports it
        send_thinking = thinking and self.supports_thinking
        if send_thinking:
            payload["thinking"] = {"type": "enabled"}
            payload["reasoning_effort"] = reasoning_effort

        response = await self.client.post(url, headers=headers, json=payload)

        # Retry without thinking params if provider rejects them
        if send_thinking and response.status_code in (400, 422):
            logger.warning(
                f"[{self.provider_name}] Model {model_id} rejected thinking params; retrying without."
            )
            payload.pop("thinking", None)
            payload.pop("reasoning_effort", None)
            response = await self.client.post(url, headers=headers, json=payload)

        # Model not found — try fallback
        if response.status_code == 404:
            logger.warning(f"[{self.provider_name}] Model {model_id} not found, trying fallback...")
            if self._fallback_model and self._fallback_model != model_id:
                payload["model"] = self._fallback_model
                payload.pop("thinking", None)
                payload.pop("reasoning_effort", None)
                response = await self.client.post(url, headers=headers, json=payload)

        if response.status_code == 402:
            raise CreditsExhaustedError(
                f"[{self.provider_name}] API credits exhausted (HTTP 402). "
                "Top up your account and restart Pragma."
            )

        if response.status_code in (401, 403):
            detail = ""
            try:
                body = response.json()
                if isinstance(body, dict):
                    detail = body.get("detail") or body.get("message") or ""
            except Exception:
                pass
            raise RuntimeError(
                f"[{self.provider_name}] Authentication failed (HTTP {response.status_code}): {detail}. "
                "Check your API key in Settings."
            )

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
