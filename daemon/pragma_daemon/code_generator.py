import json
import logging

from pragma_daemon.conformance import strip_code_fences
from pragma_daemon.deepseek import DeepSeekClient

logger = logging.getLogger(__name__)


async def generate_file(
    client: DeepSeekClient,
    file_contract: dict,
    profile: dict,
    dependency_contents: dict,
    spec_summary: str,
) -> dict:
    """Returns dict with 'content' (str) and 'usage' (dict) — real token counts."""

    system_prompt = _build_codegen_system_prompt(profile)
    messages = _build_file_messages(system_prompt, file_contract, dependency_contents, spec_summary)

    response = await client.chat(messages=messages, thinking=False)

    # The model may emit a prose preamble and/or markdown fences; extract the
    # actual source robustly.
    content = strip_code_fences(response.content.strip())

    return {
        "content": content,
        "usage": {
            "input_tokens": response.usage.input_tokens,
            "output_tokens": response.usage.output_tokens,
            "cached_input_tokens": response.usage.cached_input_tokens,
        },
    }

async def generate_readme(
    client: DeepSeekClient,
    spec: dict,
) -> str:
    prompt = f"""You are writing a README for a person who is NOT a software engineer.
They can copy-paste terminal commands but do not understand programming.

Include:
1. What this project IS
2. Prerequisites
3. How to start it
4. How to verify it's working
5. How to stop it
6. Common problems and fixes

IMPORTANT FORMATTING RULES:
- The README MUST include an exact terminal command to start the app (e.g., 'Open Terminal. Type exactly: python main.py').
- The README MUST include the exact URL where the app will be accessible (e.g., 'Your app opens at: http://localhost:8000').
- Step-by-step setup commands that a non-developer can follow. Use the format: 'Open Terminal. Type exactly: [command]. Press Enter.' for each step.
- Never assume the reader knows what a "dependency" or "environment variable" is. Explain everything in plain English.

Project Spec: {json.dumps(spec.get("setup", {}))}
Project Name: {spec.get("project_name", "")}
Description: {spec.get("description", "")}
Language: {spec.get("language", "unknown")}
Dependencies: {json.dumps(spec.get("dependencies", [])[:10])}

Output ONLY the Markdown content.
"""
    response = await client.chat([{"role": "user", "content": prompt}], thinking=False)
    return response.content.strip()

def _build_codegen_system_prompt(profile: dict) -> str:
    meta = profile.get("meta", {})
    patterns = profile.get("patterns", {}).get("context", "")
    engineering = profile.get("engineering", {}).get("context", "")
    security = profile.get("security", {}).get("context", "")
    return f"""You are a code generator. Produce ONLY the file content. No markdown fences.
No explanations. No prose before or after. Follow the contract exactly.

Stack: {meta.get("name", "Unknown")} ({meta.get("language", "")})

Framework patterns:
{patterns}

Engineering standards:
{engineering}

Security standards:
{security}
"""

def _build_file_messages(
    system: str,
    file_contract: dict,
    dependency_contents: dict,
    spec_summary: str,
) -> list[dict]:
    
    deps_str = "\n".join([f"--- {k} ---\n{v}" for k, v in dependency_contents.items()])
    
    return [
        {"role": "system", "content": system},
        {"role": "user", "content": f"Spec Summary: {spec_summary}"},
        {"role": "user", "content": f"Dependencies Context:\n{deps_str}"},
        {"role": "user", "content": f"File Contract:\n{json.dumps(file_contract, indent=2)}\n\nGenerate the file."}
    ]
