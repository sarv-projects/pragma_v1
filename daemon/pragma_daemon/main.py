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

    api_key = os.getenv("DEEPSEEK_API_KEY")
    if not api_key:
        logger.error("No API key found. Set DEEPSEEK_API_KEY.")
        sys.exit(1)

    ds_client = DeepSeekClient(api_key=api_key)

    # Groq — required for image analysis (Groq Scout / Llama 4), also accelerates
    # the interview and heal loops, and powers web-search research.
    groq_key = os.getenv("GROQ_API_KEY")
    if not groq_key:
        logger.error("Groq API key is required. Set GROQ_API_KEY environment variable.")
        sys.exit(1)
    
    groq_client = GroqClient(api_key=groq_key)
    logger.info("Groq: configured (image analysis, interview, heal, research)")

    # Discover and cache the available DeepSeek models (24h cache).
    await ds_client.ensure_model_cache()
    logger.info(
        f"Provider: DeepSeek\n"
        f"  Reasoning model: {ds_client._reasoning_model}\n"
        f"  Codegen model:   {ds_client._codegen_model}\n"
        f"  Fallback model:  {ds_client._fallback_model}"
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

    try:
        await server.serve()
    except asyncio.CancelledError:
        logger.info("Server cancelled")
    finally:
        logger.info("Server stopped")


if __name__ == "__main__":
    asyncio.run(main())
