import argparse
import asyncio
import logging
import os
import sys
from pathlib import Path

from pragma_daemon.cache import L1Cache
from pragma_daemon.deepseek import DeepSeekClient
from pragma_daemon.groq_client import GroqClient
from pragma_daemon.methods import RPCMethods
from pragma_daemon.openai_compat import OpenAICompatClient
from pragma_daemon.rpc import RPCServer


def setup_logging():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        handlers=[logging.StreamHandler(sys.stdout)],
    )


async def main():
    setup_logging()
    logger = logging.getLogger("pragma_daemon")

    parser = argparse.ArgumentParser()
    parser.add_argument("--socket", required=True, help="Path to Unix socket")
    args = parser.parse_args()

    # Read BYOK provider config from environment variables
    provider_name = os.getenv("PRAGMA_PROVIDER_NAME", "deepseek")
    provider_base_url = os.getenv("PRAGMA_PROVIDER_BASE_URL", "https://api.deepseek.com")
    provider_reasoning_model = os.getenv("PRAGMA_PROVIDER_REASONING_MODEL", "")
    provider_codegen_model = os.getenv("PRAGMA_PROVIDER_CODEGEN_MODEL", "")
    provider_supports_thinking = os.getenv("PRAGMA_PROVIDER_SUPPORTS_THINKING", "true").lower() == "true"

    # Create the codegen LLM client based on provider config
    # DeepSeek gets its own optimized client; everything else uses the generic OpenAI-compat client
    # PROVIDER_API_KEY is the BYOK key; fall back to DEEPSEEK_API_KEY for backward compat
    provider_api_key = os.getenv("PROVIDER_API_KEY") or os.getenv("DEEPSEEK_API_KEY")
    if not provider_api_key:
        logger.error("No API key found. Set DEEPSEEK_API_KEY or PROVIDER_API_KEY (for BYOK providers).")
        sys.exit(1)

    if provider_name == "deepseek":
        ds_client = DeepSeekClient(api_key=provider_api_key, base_url=provider_base_url)
        logger.info(f"Codegen provider: DeepSeek ({provider_base_url})")
    else:
        # BYOK: use the generic OpenAI-compatible client
        ds_client = OpenAICompatClient(
            api_key=provider_api_key,
            base_url=provider_base_url,
            provider_name=provider_name,
            reasoning_model=provider_reasoning_model,
            codegen_model=provider_codegen_model,
            supports_thinking=provider_supports_thinking,
        )
        logger.info(f"Codegen provider: {provider_name} ({provider_base_url})")

    # Groq — optional but recommended. Powers image analysis (Groq Scout / Llama 4),
    # accelerates the interview and heal loops, and powers web-search research.
    # Without it, image upload is disabled and interview/healing fall back to DeepSeek.
    groq_key = os.getenv("GROQ_API_KEY")
    groq_client = None
    if groq_key:
        groq_client = GroqClient(api_key=groq_key)
        logger.info("Groq: configured (image analysis, interview, heal, research)")
    else:
        logger.warning("Groq: not configured — image analysis disabled, interview/healing will use DeepSeek")

    # Discover and cache the available models (24h cache).
    await ds_client.ensure_model_cache()
    logger.info(
        f"Provider: {provider_name}\n"
        f"  Reasoning model: {ds_client._reasoning_model}\n"
        f"  Codegen model:   {ds_client._codegen_model}"
    )

    # Persistent, per-project cache.  Keyed by input content so identical
    # prompts hit cache, but different projects never share results because
    # the manifest/messages differ.  Persists across daemon restarts so
    # resumed runs don't re-call the LLM for previously-answered questions.
    cache_dir = Path.home() / ".pragma"
    cache_dir.mkdir(parents=True, exist_ok=True)
    cache = L1Cache(max_entries=500, persist_path=cache_dir / "cache.json")
    logger.info(
        f"Cache: persistent at {cache.persist_path} ({cache.size} entries loaded)"
    )
    methods = RPCMethods(ds_client, cache, groq=groq_client)

    # DeepSeek has no RPM cap, so allow high server-side concurrency.
    max_concurrency = 20
    server = RPCServer(args.socket, max_concurrency=max_concurrency)
    server.register("ping", methods.ping)
    server.register("interview_chat", methods.interview_chat)
    server.register("do_research", methods.do_research)
    server.register("compile_spec", methods.compile_spec)
    server.register("generate_file", methods.generate_file)
    server.register("generate_readme", methods.generate_readme)
    server.register("check_coverage", methods.check_coverage)
    server.register("discover_models", methods.discover_models)
    server.register("security_audit", methods.security_audit)
    server.register("static_analysis", methods.static_analysis)
    server.register("extend_project", methods.extend_project)
    server.register("analyze_image", methods.analyze_image)
    server.register("apply_delta", methods.apply_delta)
    server.register("fix_runtime_error", methods.fix_runtime_error)

    try:
        await server.serve()
    except asyncio.CancelledError:
        logger.info("Server cancelled")
    finally:
        logger.info("Server stopped")


if __name__ == "__main__":
    asyncio.run(main())
