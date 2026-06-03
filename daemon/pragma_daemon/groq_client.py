"""
Groq client for Pragma.

Groq is the OPTIONAL, free provider. When a GROQ_API_KEY is configured it
accelerates two phases that don't need DeepSeek's reasoning depth:

  - Ideation chat  -> llama-3.3-70b-versatile (best conversational model on Groq)
  - Healing loop   -> openai/gpt-oss-20b (~1000 t/s, ideal for short targeted fixes)

It also exposes compound_search() (Tavily-powered web search via groq/compound)
for the research phase. Everything here degrades gracefully: if Groq is absent
or a model is unavailable, the caller falls back to DeepSeek.

All network calls go through _chat() which retries on 429/5xx with exponential
backoff (tenacity).
"""

import logging
from dataclasses import dataclass

import httpx
from tenacity import (
    before_sleep_log,
    retry,
    retry_if_exception,
    stop_after_attempt,
    wait_exponential,
)

logger = logging.getLogger(__name__)

GROQ_BASE_URL = "https://api.groq.com/openai/v1"


def _is_retryable(e: Exception) -> bool:
    """Retry only on rate limits (429) and transient server errors (5xx)."""
    if isinstance(e, httpx.TimeoutException):
        return True
    if isinstance(e, httpx.HTTPStatusError):
        return e.response.status_code == 429 or e.response.status_code >= 500
    return False


@dataclass
class GroqResponse:
    content: str
    model: str
    input_tokens: int
    output_tokens: int


class GroqClient:
    """Thin async wrapper over Groq's OpenAI-compatible chat API."""

    def __init__(self, api_key: str):
        self.api_key = api_key
        # Longer timeout than usual: compound_search() triggers a live web search.
        self.client = httpx.AsyncClient(timeout=90.0)

    async def close(self):
        """Close the underlying HTTP client to release resources."""
        if self.client:
            await self.client.aclose()

    @retry(
        wait=wait_exponential(multiplier=1, min=2, max=30, max_jitter=5),
        stop=stop_after_attempt(3),
        retry=retry_if_exception(_is_retryable),
        before_sleep=before_sleep_log(logger, logging.WARNING),
    )
    async def _chat(
        self,
        model: str,
        messages: list[dict],
        max_tokens: int = 4096,
        temperature: float = 0.3,
        tools: list | None = None,
        response_format: dict | None = None,
    ) -> GroqResponse:
        """Raw chat call to Groq. Retries on 429/5xx."""
        payload: dict = {
            "model": model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
        }
        if tools:
            payload["tools"] = tools
        if response_format:
            payload["response_format"] = response_format

        resp = await self.client.post(
            f"{GROQ_BASE_URL}/chat/completions",
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
            },
            json=payload,
        )
        resp.raise_for_status()
        data = resp.json()
        choice = data["choices"][0]["message"]
        usage = data.get("usage", {})

        return GroqResponse(
            content=choice.get("content", "") or "",
            model=data.get("model", model),
            input_tokens=usage.get("prompt_tokens", 0),
            output_tokens=usage.get("completion_tokens", 0),
        )

    async def compound_search(self, query: str, max_tokens: int = 2048) -> str:
        """Use Groq Compound (Tavily-powered web search) to get current info.

        Tries groq/compound first, falls back to groq/compound-mini. Returns an
        empty string if both fail — research is best-effort, never fatal.
        """
        for model in ["groq/compound", "groq/compound-mini"]:
            try:
                resp = await self._chat(
                    model=model,
                    messages=[{"role": "user", "content": query}],
                    max_tokens=max_tokens,
                    temperature=0.3,
                )
                return resp.content
            except Exception as e:
                logger.warning(f"{model} failed: {e}")
        return ""

    async def heal_code(
        self, messages: list[dict], max_tokens: int = 8192
    ) -> GroqResponse:
        """Fast code healing using gpt-oss-20b (~1000 t/s).

        Falls back gpt-oss-20b -> gpt-oss-120b -> llama-3.3-70b on failure so a
        single model outage never blocks the heal loop.
        """
        for model in ["openai/gpt-oss-20b", "openai/gpt-oss-120b"]:
            try:
                return await self._chat(
                    model=model,
                    messages=messages,
                    max_tokens=max_tokens,
                    temperature=0.0,
                )
            except Exception as e:
                logger.warning(f"{model} heal failed: {e}")
        # Final fallback to llama
        return await self._chat(
            model="llama-3.3-70b-versatile",
            messages=messages,
            max_tokens=max_tokens,
            temperature=0.0,
        )

    async def chat(
        self,
        messages: list[dict],
        purpose: str = "interview",
        max_tokens: int = 2048,
        temperature: float = 0.7,
    ) -> GroqResponse:
        """Purpose-routed chat used by the RPC layer.

        ``purpose="interview"`` -> llama-3.3-70b-versatile (best conversational
        model on Groq). ``purpose="heal"`` -> delegates to heal_code().
        The caller is expected to have already prepended any system message.
        """
        if purpose == "heal":
            return await self.heal_code(messages, max_tokens=max_tokens)

        # Default / interview
        return await self._chat(
            model="llama-3.3-70b-versatile",
            messages=messages,
            max_tokens=max_tokens,
            temperature=temperature,
        )

    async def vision_chat(
        self,
        image_base64: str,
        prompt: str,
        max_tokens: int = 2048,
        mime_type: str = "image/jpeg",
        json_mode: bool = False,
    ) -> GroqResponse:
        """Analyze an image using Llama 4 Scout vision model on Groq.

        IMPORTANT: Images are ONLY sent to Groq (llama-4-scout-17b-16e-instruct).
        They are NEVER sent to DeepSeek (api.deepseek.com does not support images).

        Args:
            image_base64: Base64-encoded image bytes (no data: URI prefix).
            prompt: Text instruction for the model.
            max_tokens: Maximum response tokens.
            mime_type: MIME type of the image (image/jpeg, image/png, image/webp).
            json_mode: If True, enable response_format=json_object for more reliable JSON parsing.

        Returns:
            GroqResponse with JSON content describing the image.
        """
        model = "meta-llama/llama-4-scout-17b-16e-instruct"
        messages = [
            {
                "role": "user",
                "content": [
                    {
                        "type": "image_url",
                        "image_url": {
                            "url": f"data:{mime_type};base64,{image_base64}",
                        },
                    },
                    {
                        "type": "text",
                        "text": prompt,
                    },
                ],
            }
        ]
        
        # Use response_format for JSON mode if supported by the model
        # Llama 4 Scout supports response_format: {"type": "json_object"}
        response_format = {"type": "json_object"} if json_mode else None
        
        return await self._chat(
            model=model,
            messages=messages,
            max_tokens=max_tokens,
            temperature=0.1,
            response_format=response_format,
        )
