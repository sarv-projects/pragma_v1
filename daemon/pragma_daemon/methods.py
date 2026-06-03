import json
import logging
import os
import re
from pathlib import Path

from pragma_daemon.cache import L1Cache
from pragma_daemon.code_generator import generate_file
from pragma_daemon.conformance import check_conformance, heal_file, strip_code_fences
from pragma_daemon.deepseek import DeepSeekClient
from pragma_daemon.groq_client import GroqClient
from pragma_daemon.research import resolve_research
from pragma_daemon.spec_compiler import SpecCompilationError, compile_spec
from pragma_daemon.spec_validator import fatal_errors, validate_spec

logger = logging.getLogger(__name__)

INTERVIEW_SYSTEM = """You are a senior software architect conducting a scoping interview for a new project.
Ask diagnostic questions ONE AT A TIME (Max 3 questions per turn). to determine:
- Core purpose and user stories
- Data models and relationships
- API endpoints and integrations
- Authentication and authorization
- Third-party services (Stripe, SendGrid, etc.)

When you have enough information, respond with EXACTLY the marker [SCOPING_COMPLETE] followed by
a JSON manifest object on the next line with keys: description, endpoints, data_models, integrations, auth.
Do NOT ask more than 10 questions total."""

# Developer terms used to detect project complexity from user messages.
_DEVELOPER_TERMS = {
    "api",
    "endpoint",
    "orm",
    "middleware",
    "microservice",
    "jwt",
    "redis",
    "graphql",
    "websocket",
    "docker",
    "kubernetes",
    "cicd",
    "oauth",
    "rest",
    "grpc",
    "sdk",
    "cli",
    "deployment",
    "schema",
    "migration",
}


def _extract_json(text: str) -> str:
    """Extract the first valid JSON object or array from text, handling nested structures and string literals."""
    start_obj = text.find('{')
    start_arr = text.find('[')
    
    if start_obj == -1 and start_arr == -1:
        return ""
        
    if start_obj != -1 and (start_arr == -1 or start_obj < start_arr):
        start = start_obj
        end_char = '}'
        start_char = '{'
    else:
        start = start_arr
        end_char = ']'
        start_char = '['

    count = 0
    in_string = False
    escape_next = False
    
    for i in range(start, len(text)):
        char = text[i]
        
        if escape_next:
            escape_next = False
            continue
            
        if char == '\\':
            escape_next = True
            continue
            
        if char == '"' and not escape_next:
            in_string = not in_string
            continue
            
        if not in_string:
            if char == start_char:
                count += 1
            elif char == end_char:
                count -= 1
                if count == 0:
                    return text[start:i+1]
                    
    return ""


class RPCMethods:
    def __init__(
        self, deepseek: DeepSeekClient, cache: L1Cache, groq: GroqClient | None = None
    ):
        self.ds = deepseek
        self.cache = cache
        self.groq = groq

    async def ping(self) -> str:
        return "pong"

    async def discover_models(self) -> list[str]:
        return await self.ds.ensure_model_cache()

    async def interview_chat(self, messages: list[dict]) -> dict:
        """
        Send interview conversation to LLM.
        Uses Groq (llama-3.3-70b-versatile) if a key is present — better
        conversational quality and free. Falls back to DeepSeek otherwise.
        """
        logger.info(f"Interview chat: {len(messages)} messages")
        full_messages = [{"role": "system", "content": INTERVIEW_SYSTEM}] + messages

        # Use Groq for interview if available — better conversational model
        if self.groq:
            logger.info("Interview: using Groq (llama-3.3-70b-versatile)")
            resp = await self.groq.chat(
                full_messages, purpose="interview", max_tokens=2048, temperature=0.7
            )
            content = resp.content
            input_tokens = resp.input_tokens
            output_tokens = resp.output_tokens
        else:
            logger.info("Interview: using DeepSeek (fast model, no thinking)")
            # Interview is conversational — use the fast codegen model with no
            # reasoning. Chain-of-thought would add 30-90s of latency per chat
            # turn, which is unacceptable for an interactive interview.
            ds_resp = await self.ds.chat(
                full_messages,
                thinking=False,
                max_tokens=2048,
                temperature=0.7,
            )
            content = ds_resp.content
            input_tokens = ds_resp.usage.input_tokens
            output_tokens = ds_resp.usage.output_tokens

        done = "[SCOPING_COMPLETE]" in content
        manifest = None
        if done:
            parts = content.split("[SCOPING_COMPLETE]")
            json_part = parts[-1].strip()
            try:
                manifest = json.loads(json_part)
            except json.JSONDecodeError:
                extracted = _extract_json_braces(json_part)
                if extracted:
                    try:
                        manifest = json.loads(extracted)
                    except json.JSONDecodeError:
                        manifest = {"description": json_part}

        # Step 4/11: Detect complexity from user messages and inject into manifest.
        complexity = _detect_complexity(messages)
        if done and manifest is not None:
            manifest["complexity"] = complexity
            num_endpoints = len(manifest.get("endpoints", []))
            num_models = len(manifest.get("data_models", []))
            if num_endpoints + num_models > 25:
                content += f"\n\n⚠️ This is a large project (~{num_endpoints} endpoints, {num_models} models). Pragma works best with projects under 20 files. Consider breaking this into smaller pieces, or expect generation to take longer and cost more ($0.10+)."

        result = {
            "content": content,
            "done": done,
            "manifest": manifest,
            "usage": {"input_tokens": input_tokens, "output_tokens": output_tokens},
        }
        return result

    async def refine_spec(self, manifest: dict, prompt: str) -> dict:
        """Modify an existing manifest based on user feedback."""
        logger.info(f"Refining spec with prompt: {prompt}")
        sys_prompt = (
            "You are a senior software architect modifying an existing project manifest.\n"
            "You will be given the current JSON manifest and a user's instruction for changes.\n"
            "Output ONLY the new JSON manifest. Maintain the exact same schema structure."
        )
        msg = f"Current manifest:\n{json.dumps(manifest, indent=2)}\n\nUser request: {prompt}"
        
        try:
            if self.groq:
                resp = await self.groq.chat(
                    [{"role": "system", "content": sys_prompt}, {"role": "user", "content": msg}],
                    purpose="healing", max_tokens=4096
                )
            else:
                resp = await self.ds.chat(
                    [{"role": "system", "content": sys_prompt}, {"role": "user", "content": msg}],
                    thinking=False, max_tokens=4096
                )
            
            text = resp.content.strip()
            extracted = _extract_json_braces(text)
            if extracted:
                return json.loads(extracted)
            return json.loads(text)
        except Exception as e:
            logger.warning(f"refine_spec failed: {e}")
            raise ValueError(f"Failed to refine spec: {e}") from e

    async def do_research(self, manifest: dict, profile: dict) -> dict:
        cache_key = "do_research:" + json.dumps(
            {"manifest": manifest, "profile": profile}, sort_keys=True
        )
        cached = self.cache.get(cache_key)
        if cached:
            return cached

        language = profile.get("meta", {}).get("language", "python")
        logger.info(f"Doing research for language: {language}")
        result = await resolve_research(manifest, language, groq_client=self.groq)
        self.cache.set(cache_key, result)
        return result

    async def compile_spec(self, manifest: dict, research: dict, profile: dict) -> dict:
        complexity = manifest.get("complexity", "simple")

        # NOTE: compile_spec is intentionally NOT cached. Each spec compilation
        # must be fresh because it's the most critical step and identical manifests
        # can produce different (improved) specs on subsequent runs.

        logger.info("Compiling spec...")
        spec = await compile_spec(
            self.ds, manifest, research, profile, complexity=complexity
        )

        # Validate immediately -- only STRUCTURAL problems are fatal (cycles,
        # duplicate paths, malformed shape). Advisory warnings (missing
        # signatures, soft coverage gaps) are logged but never abort the run.
        errors = validate_spec(spec, manifest)
        fatal = fatal_errors(errors)
        warnings = [e for e in errors if e.severity == "warning"]
        if warnings:
            logger.info(f"Spec validation produced {len(warnings)} advisory warning(s)")
        if fatal:
            err_strings = [
                f"{e.rule}: {e.message} (file: {e.file_path})" for e in fatal
            ]
            logger.warning(f"Spec validation failed (fatal): {err_strings}")
            raise SpecCompilationError(
                f"Generated spec failed validation: {err_strings}"
            )

        # Generate a plain-English summary (cheap non-thinking call).
        summary = await self._generate_spec_summary(spec)
        spec["_summary"] = summary

        return spec

    async def _generate_spec_summary(self, spec: dict) -> str:
        """Generate a 3-5 sentence plain-English summary of the spec."""
        project_name = spec.get("project_name", "project")
        description = spec.get("description", "")
        language = spec.get("language", "unknown")
        file_count = len(spec.get("files", []))
        deps = spec.get("dependencies", [])

        prompt = (
            f"Summarize this software project in 3-5 sentences for a non-technical person:\n"
            f"Project: {project_name}\n"
            f"Description: {description}\n"
            f"Language: {language}\n"
            f"Number of files: {file_count}\n"
            f"Key dependencies: {', '.join(deps[:10])}\n\n"
            f"Explain what it does, its main features, and what technologies it uses. "
            f"Keep it simple and clear."
        )
        try:
            resp = await self.ds.chat(
                [{"role": "user", "content": prompt}],
                thinking=False,
                max_tokens=512,
            )
            return resp.content.strip()
        except Exception as e:
            logger.warning(f"Failed to generate spec summary: {e}")
            return f"{project_name}: {description}"

    async def generate_file(
        self, file_contract: dict, profile: dict, deps: dict, spec_summary: str
    ) -> dict:
        import hashlib
        deps_hash = hashlib.sha256(json.dumps(deps, sort_keys=True).encode()).hexdigest()
        cache_key = "generate_file:" + json.dumps(
            {
                "file": file_contract,
                "profile": profile,
                "deps_hash": deps_hash,
                "spec_summary": spec_summary,
            },
            sort_keys=True,
        )
        cached = self.cache.get(cache_key)
        if cached:
            return cached

        path = file_contract.get("path", "unknown")
        logger.info(f"Generating file: {path}")

        gen_resp = await generate_file(
            self.ds, file_contract, profile, deps, spec_summary
        )

        # Conformance check
        language = profile.get("meta", {}).get("language", "python")
        rules = profile.get("conformance_rules", {})

        violations = check_conformance(gen_resp["content"], language, rules)
        healed = False
        if violations:
            # Use Groq gpt-oss-20b for heal if available (1000 t/s, free, great for fixes)
            # Otherwise fall back to primary provider
            if self.groq:
                logger.info(
                    f"Healing {path} with Groq gpt-oss-20b ({len(violations)} violations)"
                )
                violation_texts = "\n".join(
                    [f"- Line {v.line} ({v.rule}): {v.message}" for v in violations]
                )
                heal_messages = [
                    {
                        "role": "system",
                        "content": "You are a self-healing compiler. Fix the code to resolve all violations. Output ONLY the fixed source code, no markdown, no explanation.",
                    },
                    {
                        "role": "user",
                        "content": f"Contract:\n{json.dumps(file_contract)}\n\nViolations:\n{violation_texts}\n\nOriginal code:\n{gen_resp['content']}",
                    },
                ]
                heal_resp = await self.groq.heal_code(heal_messages, max_tokens=8192)
                content = strip_code_fences(heal_resp.content)
            else:
                logger.info(
                    f"Healing {path} with primary provider ({len(violations)} violations)"
                )
                content = await heal_file(
                    self.ds, gen_resp["content"], file_contract, violations
                )
            healed = True
        else:
            content = gen_resp["content"]

        result = {
            "content": content,
            "healed": healed,
            "usage": gen_resp["usage"],  # real usage from generate_file
        }
        self.cache.set(cache_key, result)
        return result

    async def generate_readme(self, spec: dict) -> dict:
        logger.info("Generating README...")
        # generate_readme returns just the content string; we need usage from the LLM call
        # Call the LLM directly here to capture usage
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
        resp = await self.ds.chat([{"role": "user", "content": prompt}], thinking=False)
        content = resp.content.strip()
        input_tokens = (
            resp.usage.input_tokens if hasattr(resp, "usage") and resp.usage else 0
        )
        output_tokens = (
            resp.usage.output_tokens if hasattr(resp, "usage") and resp.usage else 0
        )
        return {
            "content": content,
            "usage": {
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cached_input_tokens": 0,
            },
        }

    async def check_coverage(
        self,
        spec: dict,
        manifest: dict,
        output_dir: str | None = None,
        files_completed: list[str] | None = None,
    ) -> dict:
        """§11.5 Spec Coverage Gate — run after codegen to verify completeness.

        When ``output_dir`` is provided, this inspects the files actually
        written to disk: every spec file must exist and every declared export
        name must appear in the corresponding source. Without a directory it
        falls back to the manifest-level soft check.
        """
        logger.info("Running post-codegen coverage gate...")
        issues: list[str] = []
        spec_files = [
            f for f in spec.get("files", []) if isinstance(f, dict) and f.get("path")
        ]
        total_checks = 0

        if output_dir:
            base = Path(output_dir)
            for f in spec_files:
                rel = f["path"]
                total_checks += 1
                fpath = base / rel
                if not fpath.exists():
                    issues.append(f"MISSING_FILE: {rel}")
                    continue
                try:
                    source = fpath.read_text(encoding="utf-8", errors="ignore")
                except Exception:
                    source = ""
                for export in f.get("exports", []) or []:
                    name = (
                        export
                        if isinstance(export, str)
                        else (export.get("name") if isinstance(export, dict) else None)
                    )
                    if not name:
                        continue
                    total_checks += 1
                    if not re.search(r"\b" + re.escape(name) + r"\b", source):
                        issues.append(f"MISSING_EXPORT: {rel} -> {name}")
        else:
            errors = validate_spec(spec, manifest)
            coverage_errors = [e for e in errors if e.rule == "coverage_gate"]
            total_checks = len(manifest.get("endpoints", [])) + len(
                manifest.get("data_models", [])
            )
            issues = [e.message for e in coverage_errors]

        if files_completed is not None and len(files_completed) == 0:
            issues.append("No files generated")

        if total_checks <= 0:
            total_checks = max(1, len(spec_files))

        return {
            "passed": len(issues) == 0,
            "total_checks": total_checks,
            "issues": issues,
        }

    async def security_audit(self, files_completed: list[str], output_dir: str) -> dict:
        """Scan generated files for common security issues using a cheap LLM call."""
        logger.info(f"Running security audit on {output_dir}")
        base = Path(output_dir).resolve()
        if not base.exists():
            return {"warnings": ["Output directory does not exist"], "scanned_files": 0}

        # Prioritize security-relevant files: routes, auth, config
        priority_keywords = [
            "route",
            "auth",
            "config",
            "middleware",
            "secret",
            "env",
            "main",
            "app",
            "server",
        ]
        all_files = [f for f in files_completed if f] if files_completed else []

        # Sort by priority - files matching keywords come first
        def priority_score(path: str) -> int:
            p = path.lower()
            for i, kw in enumerate(priority_keywords):
                if kw in p:
                    return i
            return len(priority_keywords)

        sorted_files = sorted(all_files, key=priority_score)[:10]

        file_contents = []
        for rel_path in sorted_files:
            # Security: validate path doesn't escape base directory
            fpath = (base / rel_path).resolve()
            if not str(fpath).startswith(str(base)):
                logger.warning(f"Path traversal attempt blocked: {rel_path}")
                continue
            if fpath.exists() and fpath.is_file():
                try:
                    content = fpath.read_text(encoding="utf-8", errors="ignore")[:4000]
                    file_contents.append(f"--- {rel_path} ---\n{content}")
                except Exception:
                    continue

        if not file_contents:
            return {"warnings": [], "scanned_files": 0}

        prompt = (
            "Scan the following source files for security issues. Check for:\n"
            "1. Hardcoded secrets (API keys, passwords, tokens in source)\n"
            "2. Missing password hashing (plain-text password storage)\n"
            "3. No input validation (unsanitized user input)\n"
            "4. Exposed endpoints without authentication\n\n"
            "Return a JSON array of warning strings. If no issues found, return [].\n"
            "Output ONLY the JSON array, no other text.\n\n"
            + "\n\n".join(file_contents)
        )

        input_tokens = 0
        output_tokens = 0
        try:
            resp = await self.ds.chat(
                [{"role": "user", "content": prompt}],
                thinking=False,
                max_tokens=2048,
            )
            input_tokens = (
                resp.usage.input_tokens if hasattr(resp, "usage") and resp.usage else 0
            )
            output_tokens = (
                resp.usage.output_tokens if hasattr(resp, "usage") and resp.usage else 0
            )
            # Parse the JSON array from the response
            text = resp.content.strip()
            # Try to extract JSON array
            if text.startswith("["):
                warnings = json.loads(text)
            else:
                extracted = _extract_json_braces(text, '[', ']')
                if extracted:
                    warnings = json.loads(extracted)
                else:
                    warnings = [text] if text else []
        except Exception as e:
            logger.warning(f"Security audit LLM call failed: {e}")
            warnings = [f"Security audit failed: {e}"]

        return {
            "warnings": warnings,
            "scanned_files": len(sorted_files),
            "usage": {
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cached_input_tokens": 0,
            },
        }

    async def static_analysis(self, output_dir: str, spec: dict) -> dict:
        """Use tree-sitter for zero-LLM-cost static analysis of generated files."""
        logger.info(f"Running static analysis on {output_dir}")
        base = Path(output_dir).resolve()
        if not base.exists():
            return {"issues": ["Output directory does not exist"], "files_analyzed": 0}

        issues: list[str] = []
        files_analyzed = 0

        # Collect all project file paths from spec
        spec_files = {
            f["path"]
            for f in spec.get("files", [])
            if isinstance(f, dict) and f.get("path")
        }

        # Collect all files in the output directory
        existing_files: set[str] = set()
        for fpath in base.rglob("*"):
            if fpath.is_file():
                # Security: ensure path is within base directory
                resolved = fpath.resolve()
                if not str(resolved).startswith(str(base)):
                    continue
                existing_files.add(str(fpath.relative_to(base)))

        # Collect all defined function/method names across the project
        all_definitions: set[str] = set()
        all_calls: set[str] = set()

        # Check for .env.example and collect env var references
        env_example_path = base / ".env.example"
        env_vars_declared: set[str] = set()
        if env_example_path.exists():
            try:
                for line in env_example_path.read_text(encoding="utf-8").splitlines():
                    line = line.strip()
                    if line and not line.startswith("#") and "=" in line:
                        env_vars_declared.add(line.split("=", 1)[0].strip())
            except Exception:
                pass

        env_vars_used: set[str] = set()

        try:
            import tree_sitter_python as tspython
            from tree_sitter import Language, Parser

            PY_LANGUAGE = Language(tspython.language())
            py_parser = Parser(PY_LANGUAGE)
        except Exception:
            py_parser = None

        try:
            import tree_sitter_javascript as tsjs
            from tree_sitter import Language, Parser

            JS_LANGUAGE = Language(tsjs.language())
            js_parser = Parser(JS_LANGUAGE)
        except Exception:
            js_parser = None

        for rel_path in existing_files:
            fpath = base / rel_path
            if not fpath.is_file():
                continue

            try:
                source = fpath.read_bytes()
            except Exception:
                continue

            # Python files
            if rel_path.endswith(".py") and py_parser is not None:
                files_analyzed += 1
                tree = py_parser.parse(source)
                _collect_python_info(
                    tree.root_node,
                    rel_path,
                    all_definitions,
                    all_calls,
                    env_vars_used,
                    issues,
                    spec_files,
                    base,
                )

            # JavaScript/TypeScript files
            elif rel_path.endswith((".js", ".ts")) and js_parser is not None:
                files_analyzed += 1
                tree = js_parser.parse(source)
                _collect_js_info(
                    tree.root_node,
                    rel_path,
                    all_definitions,
                    all_calls,
                    env_vars_used,
                    issues,
                    spec_files,
                    base,
                )

        # Check env vars: if .env.example exists, all used env vars should be declared
        if env_vars_declared and env_vars_used:
            missing_env = env_vars_used - env_vars_declared
            for var in sorted(missing_env):
                issues.append(
                    f"ENV_VAR_MISSING: {var} is used in code but not declared in .env.example"
                )

        return {"issues": issues, "files_analyzed": files_analyzed}

    async def extend_project(
        self,
        checkpoint_manifest: dict,
        checkpoint_spec: dict,
        new_requirements: str,
    ) -> dict:
        """Merge old manifest with new requirements and produce a delta spec."""
        logger.info(f"Extending project with: {new_requirements[:100]}...")

        prompt = (
            f"You are extending an existing project with new requirements.\n\n"
            f"Existing manifest:\n{json.dumps(checkpoint_manifest, indent=2)[:3000]}\n\n"
            f"Existing spec summary (files already generated):\n"
            f"{json.dumps([f.get('path') for f in checkpoint_spec.get('files', []) if isinstance(f, dict)][:30])}\n\n"
            f"New requirements: {new_requirements}\n\n"
            f"Produce a DELTA spec containing ONLY new or changed files needed to "
            f"fulfill the new requirements. Use the same schema as the original spec. "
            f"Include depends_on references to existing files where needed.\n"
            f"Output ONLY the JSON spec object."
        )

        try:
            resp = await self.ds.chat(
                [{"role": "user", "content": prompt}],
                thinking=True,
                reasoning_effort="medium",
                max_tokens=16384,
            )
            text = resp.content.strip()
            # Parse the delta spec
            if text.startswith("{"):
                delta_spec = json.loads(text)
            else:
                extracted = _extract_json(text)
                if extracted:
                    delta_spec = json.loads(extracted)
                else:
                    delta_spec = {"files": [], "error": "Failed to parse delta spec"}
        except Exception as e:
            logger.warning(f"extend_project failed: {e}")
            delta_spec = {"files": [], "error": str(e)}

        return delta_spec

    async def analyze_image(
        self,
        image_base64: str,
        mode: str = "ui",
    ) -> dict:
        """Analyze an image and return a structured manifest fragment.

        Uses Groq Llama 4 Scout vision model to interpret UI screenshots,
        architecture diagrams, or specification documents.

        IMPORTANT: Images are ONLY sent to Groq. They are NEVER sent to DeepSeek
        (api.deepseek.com does not support image inputs).

        Args:
            image_base64: Base64-encoded image (max 4 MB recommended).
            mode: "ui" (screenshot/mockup), "document" (spec/requirements),
                  or "diagram" (ER/architecture diagram).

        Returns:
            dict with description, endpoints, data_models, integrations, tokens_used.

        Raises:
            ValueError: If Groq is not configured (required for vision).
        """
        if not self.groq:
            raise ValueError(
                "Groq API key is required for image analysis. "
                "Add a Groq key in Settings to enable this feature."
            )

        _MODE_PROMPTS = {
            "ui": (
                "You are analyzing a UI screenshot or mockup. "
                "Extract the following and return as JSON only (no extra text):\n"
                '{"description": "plain English app description", '
                '"endpoints": ["list of REST API endpoints needed, e.g. POST /api/users"], '
                '"data_models": ["list of data models, e.g. User: id, name, email"], '
                '"integrations": ["third-party services visible, e.g. Stripe"]}'
            ),
            "document": (
                "You are analyzing a requirements document or specification. "
                "Extract the following and return as JSON only (no extra text):\n"
                '{"description": "plain English app description", '
                '"endpoints": ["list of API endpoints described"], '
                '"data_models": ["list of data models or entities"], '
                '"integrations": ["third-party services mentioned"]}'
            ),
            "diagram": (
                "You are analyzing an ER diagram or architecture diagram. "
                "Extract the following and return as JSON only (no extra text):\n"
                '{"description": "plain English system description", '
                '"endpoints": ["API endpoints implied by the architecture"], '
                '"data_models": ["entities or tables in the diagram"], '
                '"integrations": ["external services or databases shown"]}'
            ),
        }

        prompt = _MODE_PROMPTS.get(mode, _MODE_PROMPTS["ui"])
        logger.info(f"analyze_image: mode={mode}, b64_len={len(image_base64)}")

        # Use json_mode=True for more reliable JSON parsing
        # Llama 4 Scout supports response_format: {"type": "json_object"}
        response = await self.groq.vision_chat(
            image_base64=image_base64,
            prompt=prompt,
            max_tokens=1024,
            json_mode=True,
        )

        content = response.content.strip()
        result: dict = {}
        try:
            result = json.loads(content)
        except json.JSONDecodeError:
            # Try to extract a JSON object from surrounding text
            # This fallback should rarely be needed with json_mode=True
            m = re.search(r"\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}", content, re.DOTALL)
            if m:
                try:
                    result = json.loads(m.group())
                except json.JSONDecodeError:
                    logger.warning(
                        f"analyze_image: unparseable JSON in response: {content[:200]}"
                    )
                    result = {"description": content}
            else:
                result = {"description": content}

        return {
            "description": result.get("description", ""),
            "endpoints": result.get("endpoints", []),
            "data_models": result.get("data_models", []),
            "integrations": result.get("integrations", []),
            "tokens_used": response.input_tokens + response.output_tokens,
            "model": response.model,
        }


def _detect_complexity(messages: list[dict]) -> str:
    """Analyze user messages for developer terms to determine complexity level."""
    found_terms: set[str] = set()
    for msg in messages:
        if msg.get("role") != "user":
            continue
        text = (msg.get("content") or "").lower()
        # Split on word boundaries and check each word
        words = set(re.findall(r"[a-z]+", text))
        found_terms.update(words & _DEVELOPER_TERMS)
    return "advanced" if len(found_terms) >= 3 else "simple"


def _collect_python_info(
    node,
    rel_path: str,
    all_definitions: set,
    all_calls: set,
    env_vars_used: set,
    issues: list,
    spec_files: set,
    base: Path,
):
    """Walk a Python AST collecting definitions, imports, calls, and env var usage."""
    if node.type == "function_definition" or node.type == "class_definition":
        name_node = node.child_by_field_name("name")
        if name_node:
            all_definitions.add(name_node.text.decode("utf-8"))

    elif node.type == "import_from_statement":
        # Check if it looks like a local import
        module_node = node.child_by_field_name("module_name")
        if module_node:
            module_text = module_node.text.decode("utf-8")
            # Convert dotted module to path
            if not module_text.startswith(
                (
                    "os",
                    "sys",
                    "json",
                    "re",
                    "typing",
                    "pathlib",
                    "asyncio",
                    "logging",
                    "dataclasses",
                    "collections",
                    "datetime",
                    "functools",
                    "itertools",
                    "abc",
                    "unittest",
                    "pytest",
                    "httpx",
                    "fastapi",
                    "flask",
                    "django",
                    "sqlalchemy",
                    "pydantic",
                )
            ):
                # Might be a local import
                potential_path = module_text.replace(".", "/") + ".py"
                if spec_files and potential_path not in spec_files:
                    # Also check if it exists as a package
                    pkg_path = module_text.replace(".", "/") + "/__init__.py"
                    if pkg_path not in spec_files:
                        # Only report if there are enough spec files to be meaningful
                        if len(spec_files) > 3:
                            issues.append(
                                f"IMPORT_NOT_FOUND: {rel_path} imports '{module_text}' but {potential_path} not in project"
                            )

    elif node.type == "call":
        func_node = node.child_by_field_name("function")
        if func_node:
            all_calls.add(func_node.text.decode("utf-8"))
        
        text = node.text.decode("utf-8")
        if "os.getenv" in text or "os.environ.get" in text:
            match = re.search(r'[("\'](\w+)["\']', text)
            if match:
                env_vars_used.add(match.group(1))

    # Check for os.environ references
    if node.type == "subscript":
        text = node.text.decode("utf-8")
        if "os.environ" in text or "os.getenv" in text:
            # Extract the key
            match = re.search(r'[\[("\']([\w]+)["\'\])]', text)
            if match:
                env_vars_used.add(match.group(1))

    for child in node.children:
        _collect_python_info(
            child,
            rel_path,
            all_definitions,
            all_calls,
            env_vars_used,
            issues,
            spec_files,
            base,
        )


def _collect_js_info(
    node,
    rel_path: str,
    all_definitions: set,
    all_calls: set,
    env_vars_used: set,
    issues: list,
    spec_files: set,
    base: Path,
):
    """Walk a JS/TS AST collecting definitions, imports, calls, and env var usage."""
    if node.type == "function_declaration":
        name_node = node.child_by_field_name("name")
        if name_node:
            all_definitions.add(name_node.text.decode("utf-8"))

    elif node.type in ("import_statement", "import_declaration"):
        # Check for local imports (starting with ./ or ../)
        source_node = node.child_by_field_name("source")
        if source_node:
            source_text = source_node.text.decode("utf-8").strip("'\"")
            if source_text.startswith("./") or source_text.startswith("../"):
                # Resolve relative import
                from_dir = str(Path(rel_path).parent)
                potential = os.path.normpath(os.path.join(from_dir, source_text))
                # Check various extensions
                found = False
                for ext in ("", ".js", ".ts", ".tsx", ".jsx", "/index.js", "/index.ts"):
                    if (potential + ext) in spec_files:
                        found = True
                        break
                if not found and len(spec_files) > 3:
                    issues.append(
                        f"IMPORT_NOT_FOUND: {rel_path} imports '{source_text}' but not found in project"
                    )

    elif node.type == "call_expression":
        func_node = node.child_by_field_name("function")
        if func_node:
            all_calls.add(func_node.text.decode("utf-8"))

    # Check for process.env references
    if node.type == "member_expression":
        text = node.text.decode("utf-8")
        if "process.env." in text:
            match = re.search(r"process\.env\.(\w+)", text)
            if match:
                env_vars_used.add(match.group(1))

    for child in node.children:
        _collect_js_info(
            child,
            rel_path,
            all_definitions,
            all_calls,
            env_vars_used,
            issues,
            spec_files,
            base,
        )
