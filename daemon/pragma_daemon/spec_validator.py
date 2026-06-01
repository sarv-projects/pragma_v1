from dataclasses import dataclass
from typing import List, Optional

# File suffixes / names that legitimately have no language-level exports.
# Config, infra and doc files must never be flagged for "missing exports".
_NON_CODE_SUFFIXES = (
    ".md", ".txt", ".rst", ".json", ".toml", ".yaml", ".yml", ".ini", ".cfg",
    ".env", ".lock", ".gitignore", ".dockerignore", ".sql", ".sh", ".css",
    ".html", ".xml", ".conf", ".properties",
)
_NON_CODE_NAMES = (
    "dockerfile", "docker-compose.yml", "docker-compose.yaml", ".env",
    "makefile", "requirements.txt", "package.json", "go.mod", "go.sum",
    "pyproject.toml", "tsconfig.json", ".env.example", "alembic.ini",
)
_NON_CODE_ROLES = (
    "config", "infra", "infrastructure", "deployment", "docker", "env",
    "documentation", "docs", "migration", "data", "asset", "static",
)


@dataclass
class ValidationError:
    rule: str
    message: str
    file_path: Optional[str]
    severity: str = "error"  # "error" (fatal) | "warning" (advisory)


def _is_non_code_file(f: dict) -> bool:
    path = (f.get("path") or "").lower()
    role = (f.get("role") or "").lower()
    name = path.rsplit("/", 1)[-1]
    if name in _NON_CODE_NAMES:
        return True
    if path.endswith(_NON_CODE_SUFFIXES):
        return True
    if any(r in role for r in _NON_CODE_ROLES):
        return True
    return False


def _looks_like_local_dep(dep: str) -> bool:
    """Heuristic to tell a local file dependency from an external package.

    Local deps look like paths (contain a slash or a known source extension).
    External packages (``fastapi``, ``@types/node``, ``github.com/...`` is
    ambiguous but treated as external) are NOT validated against the manifest.
    """
    if not dep:
        return False
    if dep.startswith("@") or dep.startswith("github.com/"):
        return False  # scoped npm package / go module path
    return "/" in dep or dep.endswith(
        (".py", ".ts", ".tsx", ".js", ".jsx", ".go", ".sql")
    )


def validate_spec(spec: dict, manifest: dict = None) -> List[ValidationError]:
    errors: List[ValidationError] = []

    files = spec.get("files", [])
    files_by_path = {f["path"]: f for f in files if isinstance(f, dict) and "path" in f}

    # Structural top-level shape check (fatal).
    if not isinstance(files, list) or not files_by_path:
        errors.append(
            ValidationError(
                rule="schema",
                message="spec.files must be a non-empty list of objects with a 'path'",
                file_path=None,
                severity="error",
            )
        )
        return errors

    errors.extend(_detect_cycles(files_by_path))          # fatal
    errors.extend(_check_duplicate_paths(files))           # fatal
    errors.extend(_check_imports(files_by_path))           # warning
    errors.extend(_check_tests(spec))                      # warning
    errors.extend(_check_has_exports(files))               # warning
    errors.extend(_check_signature_match(files))           # warning

    if manifest:
        errors.extend(_check_coverage(spec, manifest))     # warning

    return errors


def fatal_errors(errors: List[ValidationError]) -> List[ValidationError]:
    return [e for e in errors if e.severity == "error"]


def _detect_cycles(files_by_path: dict) -> List[ValidationError]:
    errors = []
    visited = set()
    path_stack = set()

    def dfs(node: str, current_path: list):
        if node in path_stack:
            cycle = current_path[current_path.index(node):] + [node]
            errors.append(
                ValidationError(
                    rule="no_dependency_cycles",
                    message=f"Cycle detected: {' -> '.join(cycle)}",
                    file_path=node,
                    severity="error",
                )
            )
            return
        if node in visited:
            return

        visited.add(node)
        path_stack.add(node)

        f = files_by_path.get(node)
        if f:
            for dep in f.get("depends_on", []):
                if dep in files_by_path:  # only follow local edges
                    dfs(dep, current_path + [node])

        path_stack.discard(node)

    for path in files_by_path:
        dfs(path, [])

    return errors


def _check_imports(files_by_path: dict) -> List[ValidationError]:
    errors = []
    for path, f in files_by_path.items():
        for dep in f.get("depends_on", []):
            # Only validate things that look like local files. External
            # packages (fastapi, zod, pgx, ...) are expected to be absent.
            if _looks_like_local_dep(dep) and dep not in files_by_path:
                errors.append(
                    ValidationError(
                        rule="imports_resolve",
                        message=f"Local dependency {dep} not found in file manifest",
                        file_path=path,
                        severity="warning",
                    )
                )
    return errors


def _check_tests(spec: dict) -> List[ValidationError]:
    errors = []
    tests = spec.get("tests", [])
    # A file manifest may also encode tests as files named test_*/ *_test / *.test.*
    has_test_files = any(
        _is_test_path((f.get("path") or "")) for f in spec.get("files", []) if isinstance(f, dict)
    )
    if not tests and not has_test_files:
        errors.append(
            ValidationError(
                rule="has_tests",
                message="No tests defined in spec",
                file_path=None,
                severity="warning",
            )
        )
    return errors


def _is_test_path(path: str) -> bool:
    p = path.lower()
    name = p.rsplit("/", 1)[-1]
    return (
        name.startswith("test_")
        or name.endswith("_test.py")
        or name.endswith("_test.go")
        or ".test." in name
        or ".spec." in name
        or "/tests/" in p
        or "/test/" in p
    )


def _check_duplicate_paths(files: list) -> List[ValidationError]:
    errors = []
    seen = set()
    for f in files:
        if not isinstance(f, dict):
            continue
        path = f.get("path")
        if path in seen:
            errors.append(
                ValidationError(
                    rule="no_duplicate_paths",
                    message=f"Duplicate file path: {path}",
                    file_path=path,
                    severity="error",
                )
            )
        seen.add(path)
    return errors


def _check_coverage(spec: dict, manifest: dict) -> List[ValidationError]:
    """Soft coverage check against the requirements manifest.

    Endpoints are matched by their non-parameterised path segments (so
    ``/users/{id}`` matches a route declared as ``/users/:id`` or
    ``/users/<id>``). Data models are matched case-insensitively. Misses are
    warnings, never fatal — the authoritative gate runs against generated
    files (see verify_spec_coverage in conformance/coverage).
    """
    errors = []
    spec_str = str(spec).lower()

    for ep in manifest.get("endpoints", []):
        if not isinstance(ep, dict):
            continue
        path = (ep.get("path") or "").lower()
        if not path:
            continue
        # Compare on the literal (non-param) segments only.
        segments = [seg for seg in path.split("/") if seg and not _is_param_segment(seg)]
        if segments and not all(seg in spec_str for seg in segments):
            errors.append(
                ValidationError(
                    rule="coverage_gate",
                    message=f"Endpoint {path} from manifest may not be covered in the spec",
                    file_path=None,
                    severity="warning",
                )
            )

    for m in manifest.get("data_models", []):
        if not isinstance(m, dict):
            continue
        name = (m.get("name") or "").lower()
        if name and name not in spec_str:
            errors.append(
                ValidationError(
                    rule="coverage_gate",
                    message=f"Data model {name} from manifest may not be covered in the spec",
                    file_path=None,
                    severity="warning",
                )
            )

    return errors


def _is_param_segment(seg: str) -> bool:
    return (
        (seg.startswith("{") and seg.endswith("}"))
        or seg.startswith(":")
        or (seg.startswith("<") and seg.endswith(">"))
    )


def _check_has_exports(files: list) -> List[ValidationError]:
    errors = []
    for f in files:
        if not isinstance(f, dict):
            continue
        if _is_non_code_file(f):
            continue  # config/infra/doc files legitimately have no exports
        exports = f.get("exports")
        public_api = f.get("public_api")
        if not exports and not public_api:
            errors.append(
                ValidationError(
                    rule="has_exports",
                    message="Code file declares neither exports nor public_api",
                    file_path=f.get("path"),
                    severity="warning",
                )
            )
    return errors


def _check_signature_match(files: list) -> List[ValidationError]:
    """Schema: exports are strings; signatures live in public_api[].

    Each public_api entry should have a 'name' and either 'signature' or
    'args'/'returns'. Missing signatures are advisory warnings.
    """
    errors = []
    for f in files:
        if not isinstance(f, dict):
            continue
        for api in f.get("public_api", []) or []:
            if not isinstance(api, dict):
                continue
            name = api.get("name", "unknown")
            has_sig = "signature" in api or "args" in api or "returns" in api
            if not has_sig:
                errors.append(
                    ValidationError(
                        rule="signature_match",
                        message=f"public_api entry '{name}' has no signature/args/returns",
                        file_path=f.get("path"),
                        severity="warning",
                    )
                )
    return errors
