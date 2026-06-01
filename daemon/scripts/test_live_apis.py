"""Manual smoke script for live provider connectivity.

Run directly (not under pytest) to verify your DeepSeek key and the DeepWiki
research endpoint actually work end-to-end:

    DEEPSEEK_API_KEY=sk-... python daemon/scripts/test_live_apis.py

Each check is skipped gracefully if its key is absent.
"""

import asyncio
import os
import sys
from pathlib import Path

# Add the daemon directory to sys.path so we can import pragma_daemon
sys.path.append(str(Path(__file__).parent.parent))

import logging

from pragma_daemon.deepseek import DeepSeekClient
from pragma_daemon.research import deepwiki_ask

logging.basicConfig(level=logging.INFO)


async def test_deepseek_api():
    print("--- Testing DeepSeek API ---")
    api_key = os.environ.get("DEEPSEEK_API_KEY")
    if not api_key:
        print("Skipping: DEEPSEEK_API_KEY not found in environment.")
        return
    client = DeepSeekClient(api_key=api_key)
    messages = [{"role": "user", "content": "Hello, are you there? Reply 'yes' if so."}]
    try:
        response = await client.chat(messages=messages, max_tokens=10)
        print(f"DeepSeek response: {response.content}")
        print(f"Model used: {response.model}")
    except Exception as e:
        print(f"DeepSeek API failed: {e}")


async def test_deepwiki_mcp_api():
    print("--- Testing DeepWiki MCP API ---")
    # No API key required for DeepWiki (see research.py).
    try:
        response = await deepwiki_ask("python", "What is the standard library?")
        if response:
            print(f"DeepWiki MCP response: {response[:100]}...")
        else:
            print("DeepWiki MCP returned None or empty response.")
    except Exception as e:
        print(f"DeepWiki MCP API failed: {e}")


async def main():
    await test_deepseek_api()
    print()
    await test_deepwiki_mcp_api()


if __name__ == "__main__":
    asyncio.run(main())
