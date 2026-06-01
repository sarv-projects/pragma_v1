import logging
from typing import List
import tree_sitter

try:
    import tree_sitter_python as tspython
except ImportError:
    tspython = None

try:
    import tree_sitter_javascript as tsjavascript
except ImportError:
    tsjavascript = None

try:
    import tree_sitter_go as tsgo
except ImportError:
    tsgo = None

logger = logging.getLogger(__name__)


def strip_code_fences(text: str) -> str:
    """Best-effort extraction of source code from an LLM response.

    Handles three cases:
      1. A fenced block (```lang ... ```) anywhere in the text -> return its body.
      2. The whole response wrapped in a leading/trailing fence.
      3. A short prose preamble ("Here is the code:") before raw code.
    """
    import re

    if not text:
        return text

    # Case 1/2: a fenced block anywhere — take the first one.
    match = re.search(r"```[a-zA-Z0-9_+-]*\n(.*?)```", text, re.DOTALL)
    if match:
        return match.group(1).strip("\n")

    stripped = text.strip()
    # Leading fence with no closing fence (truncated) — drop the first line.
    if stripped.startswith("```"):
        lines = stripped.split("\n")
        lines = lines[1:]
        if lines and lines[-1].strip().startswith("```"):
            lines = lines[:-1]
        return "\n".join(lines)

    return text


def _make_parser(lang: "tree_sitter.Language") -> "tree_sitter.Parser":
    """Construct a parser across tree-sitter API versions.

    tree-sitter >=0.22 removed ``Parser.set_language()`` in favour of
    ``Parser(language)`` / the ``.language`` property. Older releases still
    expose ``set_language``. Support all of them.
    """
    try:
        return tree_sitter.Parser(lang)
    except TypeError:
        parser = tree_sitter.Parser()
        if hasattr(parser, "set_language"):
            parser.set_language(lang)
        else:
            parser.language = lang
        return parser


class ConformanceViolation:
    def __init__(self, rule: str, line: int, message: str):
        self.rule = rule
        self.line = line
        self.message = message


def check_conformance(
    content: str,
    language: str,
    rules: dict,
) -> List[ConformanceViolation]:
    violations: List[ConformanceViolation] = []

    # Simple rule checks that don't need tree-sitter yet
    if rules.get("ban_any_type") and language in ["typescript", "ts", "tsx"]:
        for i, line in enumerate(content.split("\n"), 1):
            if ": any" in line or "<any>" in line or "as any" in line:
                violations.append(ConformanceViolation("ban_any_type", i, "Usage of 'any' type is banned"))

    if rules.get("require_use_client_for_hooks") and language in ["typescript", "tsx", "react"]:
        has_hooks = False
        has_use_client = False
        for line in content.split("\n"):
            if line.startswith('"use client"') or line.startswith("'use client'"):
                has_use_client = True
            if "useState(" in line or "useEffect(" in line:
                has_hooks = True

        if has_hooks and not has_use_client:
            violations.append(
                ConformanceViolation(
                    "require_use_client_for_hooks", 1, "File uses hooks but missing 'use client' directive"
                )
            )

    # Basic syntax check with tree-sitter
    lang = None
    parser_missing = False

    if language in ["python", "py"]:
        if tspython:
            lang = tree_sitter.Language(tspython.language())
        else:
            parser_missing = True
    elif language in ["javascript", "typescript", "js", "ts", "jsx", "tsx"]:
        if tsjavascript:
            lang = tree_sitter.Language(tsjavascript.language())
        else:
            parser_missing = True
    elif language in ["go"]:
        if tsgo:
            lang = tree_sitter.Language(tsgo.language())
        else:
            parser_missing = True

    if parser_missing:
        # Missing parser is an environment issue, not a code defect — don't
        # fail the file over it, just log.
        logger.warning(f"tree-sitter parser missing for {language}; skipping AST syntax check")
    elif lang:
        try:
            parser = _make_parser(lang)
            tree = parser.parse(bytes(content, "utf8"))
            if tree.root_node.has_error:
                violations.append(ConformanceViolation("syntax_error", 1, "AST contains syntax errors"))
        except Exception as e:
            logger.warning(f"Tree-sitter parse failed: {e}")

    return violations


async def heal_file(client, original_content: str, contract: dict, violations: List[ConformanceViolation]) -> str:
    logger.info(f"Healing file with {len(violations)} violations")

    violation_texts = "\n".join([f"- Line {v.line} ({v.rule}): {v.message}" for v in violations])

    messages = [
        {
            "role": "system",
            "content": "You are a self-healing compiler. Fix the code to resolve the AST violations. "
            "Output ONLY the fixed source code without any markdown wrapping or explanation.",
        },
        {
            "role": "user",
            "content": f"Contract:\n{contract}\n\nViolations:\n{violation_texts}\n\nOriginal Code:\n{original_content}",
        },
    ]

    # client.chat() returns a ChatResponse dataclass — use its .content field.
    resp = await client.chat(messages, max_tokens=8192, temperature=0.0, thinking=True)
    return strip_code_fences(resp.content)
