# Pragma — Technical Specification v5.2

**Status:** Authoritative  
**Last updated:** June 2026

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [System Architecture](#2-system-architecture)
3. [Pipeline Phases](#3-pipeline-phases)
4. [Phase 1: Ideation](#4-phase-1-ideation)
5. [Phase 2: Research](#5-phase-2-research)
6. [Phase 3: Spec Compilation](#6-phase-3-spec-compilation)
7. [Phase 4: Code Generation](#7-phase-4-code-generation)
8. [Phase 5: Post-Generation](#8-phase-5-post-generation)
9. [Model Routing & Providers](#9-model-routing--providers)
10. [Budget & Cost Tracking](#10-budget--cost-tracking)
11. [Checkpointing & Resume](#11-checkpointing--resume)
12. [Conformance & Healing](#12-conformance--healing)
13. [Security Model](#13-security-model)
14. [WebSocket Protocol](#14-websocket-protocol)
15. [REST API](#15-rest-api)
16. [Configuration](#16-configuration)
17. [Stack Profiles](#17-stack-profiles)
18. [Image Ingest (Groq Scout Vision)](#18-image-ingest-groq-scout-vision)
19. [Interactive Refinement](#19-interactive-refinement)
20. [Runtime Smoke Test Agent](#20-runtime-smoke-test-agent)
21. [Role-Specialized Spec Compilation](#21-role-specialized-spec-compilation)
22. [Golden Template System](#22-golden-template-system)
23. [CLI Reference](#23-cli-reference)
24. [Folder Structure](#24-folder-structure)
25. [Live Preview / Sandbox](#25-live-preview--sandbox)
26. [Iterative Chat During Generation](#26-iterative-chat-during-generation)
27. [Spec Progress Streaming](#27-spec-progress-streaming)
28. [Concurrent Run Prevention](#28-concurrent-run-prevention)
29. [Checkpoint Recovery & Validation](#29-checkpoint-recovery--validation)
30. [Budget Exhaustion UX](#30-budget-exhaustion-ux)
31. [BYOK OpenAI-Compatible Client](#31-byok-openai-compatible-client)
32. [Known Limitations](#32-known-limitations)

---

## 1. Executive Summary

Pragma is a terminal-first, browser-served software engineering engine. It accepts a natural language project description and produces a complete, buildable local codebase.

**Core properties:**

- **~$0.03/project** — BYOK with any OpenAI-compatible provider (DeepSeek recommended)
- **100% local** — code never leaves the machine; no telemetry
- **Full-stack** — APIs, databases, auth, Docker, tests, README
- **Checkpointed** — every slice saved; resume after crash, rate-limit, or power loss
- **Non-technical friendly** — plain English in, working code out
- **BYOK (Bring Your Own Key)** — use DeepSeek, OpenAI, Anthropic, Ollama, OpenRouter, Together, or any OpenAI-compatible provider
- **Live preview** — one-click dev server preview inside the browser (see §24)
- **Iterative chat during generation** — queue messages mid-build, auto-applied after generation (see §25)

**What it is not:**

- Not a deployment platform (generates Docker configs; user deploys)

---

## 2. System Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    USER BROWSER                           │
│              SvelteKit SPA (embedded in binary)           │
└──────────────────────┬───────────────────────────────────┘
                       │ WebSocket + REST (localhost:3777)
┌──────────────────────▼───────────────────────────────────┐
│                    GO BINARY (pragma)                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────┐  │
│  │  HTTP/WS    │  │   Pipeline   │  │  Budget Oracle  │  │
│  │  Server     │  │   Service    │  │  + Ledger       │  │
│  └─────────────┘  └──────┬───────┘  └─────────────────┘  │
│                          │ JSON-RPC 2.0 (Unix socket)     │
└──────────────────────────┼───────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────┐
│                  PYTHON DAEMON                             │
│  ┌────────────┐  ┌────────────┐  ┌──────────────────┐    │
│  │  DeepSeek  │  │    Groq    │  │  tree-sitter     │    │
│  │  Client    │  │  Client    │  │  AST Parser      │    │
│  └────────────┘  └────────────┘  └──────────────────┘    │
└──────────────────────────────────────────────────────────┘
                           │ HTTPS
              ┌────────────┴────────────┐
              │                         │
    ┌─────────▼──────┐       ┌──────────▼──────┐
    │  DeepSeek API  │       │   Groq API      │
    │  (paid, ~$0.03)│       │  (free, required)│
    └────────────────┘       └─────────────────┘
```

### 2.1 Go Binary

Single statically-linked binary. Responsibilities:

- Embeds compiled SvelteKit SPA via `go:embed` — no separate web server needed
- HTTP server on `localhost:3777` (configurable via `PRAGMA_PORT`)
- WebSocket hub for real-time pipeline events
- Pipeline state machine (phase transitions, human gates)
- Budget Oracle: pre-flight cost checks, per-run and lifetime caps
- Token Ledger: records actual spend per phase and project
- Checkpoint persistence: saves `RunState` to disk after every slice
- Daemon lifecycle: spawn, health-check (ping every 5s), restart on failure
- OS keyring integration for API key storage

### 2.2 Python Daemon

Subprocess spawned by the Go binary. Communicates via JSON-RPC 2.0 over a Unix domain socket at `/tmp/pragma/daemon-<PID>-<hex>.sock` (on Windows, `%TEMP%\pragma\daemon-<PID>-<hex>.sock` — AF_UNIX is supported on Windows 10 1803+).

Responsibilities:

- All LLM API calls (DeepSeek, Groq)
- Spec compilation (3-pass pipeline)
- Per-file code generation
- Conformance checking (tree-sitter AST + linter rules)
- Healing loop (fix conformance violations)
- Research resolution (DeepWiki MCP + DuckDuckGo)
- Security audit (LLM scan of generated files)
- Static analysis (tree-sitter: import resolution, env var coverage)
- L1 cache (persistent JSON, keyed by prompt hash)

### 2.3 Frontend

SvelteKit SPA compiled to static files, embedded in the Go binary at build time. Communicates with Go via:

- **WebSocket** (`/ws`) — real-time pipeline events
- **REST** (`/api/*`) — settings, status, runs, budget, logs

**Frontend stores** (`web/src/lib/stores/`):
- `ws.ts` — WebSocket store, all pipeline state (phase, messages, filesCompleted, specData, dagSlices, runResult, recentRuns)
- `settings.ts` — health data, key status, `bothKeysReady` derived store (DeepSeek + Groq both configured)

**Onboarding:** Replaced by `SetupGuide.svelte` — a 5-step slide guide (Welcome → DeepSeek → Groq → Setup Check → Ready). Both keys are required before using the app. The `bothKeysReady` gate replaces the old single-key gate.

---

## 3. Pipeline Phases

The pipeline is a linear state machine. Each phase emits a `phase_changed` WebSocket event.

| Phase | Wire name | Description | Blocks on |
|-------|-----------|-------------|-----------|
| Ideation | `ideation` | AI-driven clarifying interview | User messages |
| Pre-compile | *(client-side)* | User names project, adds notes | User clicks Proceed |
| Researching | `researching` | DeepWiki + DDG fetch | Daemon RPC |
| Compiling spec | `compiling_spec` | 3-pass spec compilation (with role-specialized prompts) | Daemon RPC (up to 15 min) |
| Spec review | `spec_review` | Human gate 1 | `approve_spec` action |
| DAG review | `dag_review` | Human gate 2 | `approve_dag` action |
| Generating | `generating` | Parallel file generation | Daemon RPCs |
| Complete | `complete` | All done | — |
| Refine | `refine` | Interactive chat-to-extend (see §19) | User-initiated from CompleteView |
| Error | `error` | Fatal failure | — |

Phase transitions are one-way except for `complete` ↔ `refine`, which users can toggle freely.

---

## 4. Phase 1: Ideation

**Go handler:** `internal/server/interview.go`  
**Daemon RPC:** `interview_chat(messages: list[dict]) -> dict`

The user describes their project. The AI asks up to 10 clarifying questions (max 3 per turn). When it has enough information, it emits `[SCOPING_COMPLETE]` followed by a JSON manifest.

**Manifest schema:**
```json
{
  "description": "string",
  "project_name": "string (optional, user can override in PreCompileView)",
  "endpoints": [{"method": "GET", "path": "/users", "description": "..."}],
  "data_models": [{"name": "User", "fields": ["id", "email", "created_at"]}],
  "integrations": ["stripe", "sendgrid"],
  "auth": "jwt",
  "complexity": "simple | advanced"
}
```

**Complexity detection:** The daemon analyzes user messages for developer terms (`api`, `jwt`, `orm`, `microservice`, etc.). 3+ unique terms → `advanced`. Otherwise → `simple`. This controls spec compiler behavior (see §6).

**PreCompileView gate:** After `[SCOPING_COMPLETE]`, the Go server waits up to 30 seconds for an `update_manifest` WebSocket action before starting the pipeline. This lets the user name the project and add last-minute notes. If no update arrives within 30s, the pipeline starts with the original manifest.

**Model routing:**
- Groq configured → `llama-3.3-70b-versatile` (free, fast, conversational)
- No Groq → DeepSeek Flash (thinking OFF, temperature 0.7)

---

## 5. Phase 2: Research

**Daemon RPC:** `do_research(manifest: dict, profile: dict) -> dict`  
**Timeout:** 90 seconds  
**Cost:** $0 (uses free APIs only)

Extracts 2-5 technology terms from the manifest and fetches current documentation.

**Resolution order:**
1. DeepWiki MCP (`https://mcp.deepwiki.com/mcp`) — structured API docs for GitHub repos
2. DuckDuckGo text search — fallback for anything not on DeepWiki

Research is best-effort. If it fails entirely, the pipeline continues with empty context. The spec compiler still works; it just won't have current library version info.

**Output:**
```json
{
  "findings": [
    {"query": "FastAPI SQLAlchemy async", "source": "deepwiki", "content": "..."},
    {"query": "JWT refresh tokens Python", "source": "duckduckgo", "content": "..."}
  ]
}
```

---

## 6. Phase 3: Spec Compilation

**Daemon RPC:** `compile_spec(manifest, research, profile) -> dict`  
**Timeout:** 15 minutes  
**Cost:** ~$0.015–$0.025 (3 DeepSeek calls)

The most critical phase. Produces a **Build Contract** — a complete specification of every file, function, class, import, and edge case. Code generation is deterministic given this contract.

### 3-Pass Pipeline (Role-Specialized)

**Pass 1 — Draft / Lead Architect (thinking ON, medium effort)**
- Role: "You are the Lead Software Architect" — focuses on architectural completeness, data models, API routes
- Full architectural reasoning
- Produces complete spec.json draft
- Uses reasoning model if available (V4 Pro), otherwise Flash

**Pass 2 — Optimize / Security & DB Expert (thinking OFF)**
- Role: "You are the Security & Database Expert" — reviews for auth gaps, SQL injection risks, missing indexes
- Reviews Pass 1 output
- Fixes dependency cycles, missing error handling, security gaps, interface mismatches
- Shared prompt prefix with Pass 1 → DeepSeek cache hit (~98% discount on cached tokens)

**Pass 3 — Finalize / Finalizer (thinking OFF, conditional)**
- Role: "You are the Finalizer" — consistent naming, valid JSON, resolved depends_on links
- Only runs if spec validator finds fatal errors after Pass 2
- Cleanup pass: consistent naming, valid JSON structure
- Skipped if Pass 2 validates cleanly (saves ~$0.005)

### Spec Schema

Every file node must conform to:
```json
{
  "path": "src/routes/auth.py",
  "role": "route | model | service | config | infra | test",
  "depends_on": ["src/services/auth_service.py"],
  "exports": ["router", "get_current_user"],
  "public_api": [
    {
      "name": "login",
      "signature": "async def login(form_data: OAuth2PasswordRequestForm) -> TokenResponse",
      "args": ["form_data: OAuth2PasswordRequestForm"],
      "returns": "TokenResponse",
      "description": "Authenticate user and return JWT tokens"
    }
  ]
}
```

Top-level spec fields: `project_name`, `description`, `language`, `dependencies`, `setup` (run/test commands), `files`, `tests`, `deployment` (dockerfile, compose).

### Complexity Constraints

- `simple`: monolithic architecture, SQLite, 8-15 files, no microservices
- `advanced`: full production architecture, PostgreSQL, Redis, proper layering

### Chained Compilation (large projects)

If the manifest has >18 endpoints + data models combined, the spec is compiled in domain modules: `core` → `models` → `services` → `routes` → `tests`. Each module receives the prior modules' file list as context to avoid duplicate paths.

### Validation

After compilation, `validate_spec()` checks:
- No circular dependencies in `depends_on` graph
- No duplicate file paths
- All `depends_on` references point to files that exist in the spec
- Dockerfile and docker-compose.yml are present
- `has_exports`: Files acting as code modules must have `exports` or `public_api` defined
- `has_tests`: Spec must include test files
- `coverage_gate`: Checks that manifest endpoints and data models appear in the spec

Fatal errors abort the run. Warnings are logged but don't block.

---

## 7. Phase 4: Code Generation

**Daemon RPC:** `generate_file(file_contract, profile, deps, spec_summary) -> dict`  
**Timeout:** 6 minutes per file  
**Concurrency:** 20 workers

### Topological Scheduling

Files are sorted by their `depends_on` graph into parallel slices. Files with no dependencies form Slice 0 and run first. Files that depend on Slice 0 outputs form Slice 1, and so on.

```
Slice 0: [config.py, constants.py, database.py]  ← no deps, run in parallel
Slice 1: [models/user.py, models/task.py]         ← depend on database.py
Slice 2: [services/auth.py, services/task.py]     ← depend on models
Slice 3: [routes/auth.py, routes/tasks.py]        ← depend on services
```

Cycles in the dependency graph are detected and the cycle remnant is appended as a final slice (best-effort generation).

### Per-File Generation

For each file:
1. Read dependency files from disk (already-generated files in the run directory)
2. Build prompt: file contract + dependency contents + spec summary + profile context
3. Call `generate_file` RPC
4. Daemon generates content, runs conformance check, heals if needed
5. Go writes file atomically (write to `.tmp`, then rename)
6. Record token usage in Budget Oracle
7. Emit `file_completed` WebSocket event

### Abort Threshold

If more than `max(3, totalFiles/10)` files fail, the run aborts. This prevents wasting budget on a fundamentally broken spec.

### Dependency Context

When generating a file, its dependencies are read from disk and passed as `deps: dict[path, content]`. This gives the model the actual generated code of upstream files, not just their spec contracts — critical for matching function signatures exactly.

---

## 8. Phase 5: Post-Generation

After all files are generated, the following run in sequence (all non-fatal):

### Coverage Gate
**RPC:** `check_coverage(spec, manifest, output_dir, files_completed)`

Verifies:
- Every file in the spec exists on disk
- Every declared export name appears in the corresponding source file

Reports `passed: bool`, `total_checks: int`, `issues: []string`.

### README Generation
**RPC:** `generate_readme(spec)`  
**Timeout:** 4 minutes

Generates a layman-friendly README. The prompt explicitly requires:
- Exact terminal commands to start the app
- The URL where the app will be accessible
- Step-by-step setup for non-developers

### Security Audit
**RPC:** `security_audit(files_completed, output_dir)`

Reads up to 10 priority files (routes, auth, config, middleware) and runs a cheap DeepSeek Flash call checking for:
- Hardcoded secrets
- Missing password hashing
- No input validation
- Exposed endpoints without authentication

Output is plain English warnings, not jargon. Results emitted as `security_audit` WebSocket event.

### Static Analysis
**RPC:** `static_analysis(output_dir, spec)`  
**Cost:** $0 (tree-sitter only, no LLM)

Uses tree-sitter to check:
- Python: local imports resolve to files in the project
- JS/TS: relative imports resolve
- Env vars used in code are declared in `.env.example`

### Test Execution

If `spec.setup.test` is defined, runs the test command in the output directory. Command must match an allowlist prefix (`pytest`, `go test`, `npm test`, `cargo test`, `make test`, `yarn test`, `pnpm test`, `vitest`, `jest`). Shell metacharacters (`;`, `&&`, `|`, `` ` ``, `$(`) are rejected.

Output truncated to 1MB. Results emitted as `test_run` WebSocket event.

### Git Init

If `git` is on PATH:
```bash
git init
git add -A
git -c user.name=Pragma -c user.email=pragma@local commit -m "Initial generation"
```

If git is not found, a warning is logged and generation continues.

---

## 9. Model Routing & Providers

### Primary Codegen Provider (BYOK)

Pragma supports **Bring Your Own Key** — you can use any OpenAI-compatible provider for code generation.

| Provider | Config name | Default Base URL | Thinking Support |
|----------|------------|------------------|------------------|
| DeepSeek | `deepseek` | `https://api.deepseek.com` | ✅ (V4 Pro/R1) |
| OpenAI | `openai` | `https://api.openai.com/v1` | ✅ (o1, o3) |
| OpenRouter | `openrouter` | `https://openrouter.ai/api/v1` | ❌ |
| Together AI | `together` | `https://api.together.xyz/v1` | ❌ |
| Ollama (local) | `ollama` | `http://localhost:11434/v1` | ❌ |
| Custom | `custom` | User-specified | User-specified |

**Provider configuration** flows through the system as:

1. **Go config** (`~/.pragma/config.toml`):
   ```toml
   [provider]
   name = "openai"
   base_url = "https://api.openai.com/v1"
   reasoning_model = "o3-mini"       # optional, overrides auto-detect
   codegen_model = "gpt-4o"         # optional, overrides auto-detect
   supports_thinking = true
   ```

2. **Go → Daemon**: Provider config is injected as environment variables (`PRAGMA_PROVIDER_NAME`, `PRAGMA_PROVIDER_BASE_URL`, `PRAGMA_PROVIDER_REASONING_MODEL`, `PRAGMA_PROVIDER_CODEGEN_MODEL`, `PRAGMA_PROVIDER_SUPPORTS_THINKING`)

3. **Daemon client**: If provider is `deepseek`, uses the optimized `DeepSeekClient`. Otherwise, uses the generic `OpenAICompatClient` (`daemon/pragma_daemon/openai_compat.py`)

Groq remains the default for interview, healing, and image analysis due to its free tier and speed.

### DeepSeek Client

| Field | Value |
|-------|-------|
| Base URL | `https://api.deepseek.com` |
| Models | `deepseek-v4-flash` (codegen), `deepseek-v4-pro` (reasoning, if available) |
| Pricing | $0.14/M input (cache miss), $0.0028/M input (cache hit), $0.28/M output |
| Context | 1,000,000 tokens |
| Concurrency | 2,500 concurrent requests |
| Retry | 4 attempts, exponential backoff (2s base, 20s max) |
| 402 handling | Raise `CreditsExhaustedError` immediately (not retryable) |
| 401/403 handling | Raise with actionable message (not retryable) |
| 404 handling | Re-discover models, retry with fallback |

**Thinking mode:** Enabled for spec Pass 1 only. Controlled by `thinking: {"type": "enabled"}` + `reasoning_effort`. If the model rejects thinking params (HTTP 400/422), retried without them.

**Model discovery:** On startup, the daemon calls `/models` and caches results for 24 hours in `~/.pragma/models.json`. Models are ranked by keyword preference lists for reasoning vs codegen tasks.

### Groq (optional, free)

| Field | Value |
|-------|-------|
| Base URL | `https://api.groq.com/openai/v1` |
| Models | `llama-3.3-70b-versatile` (ideation), `openai/gpt-oss-20b` (healing) |
| Pricing | $0 (free tier) |
| Rate limit | 30 RPM, 1,000 RPD for most models |

Used for:
- Ideation chat (better conversational quality than Flash)
- Healing loop (1,000 t/s, fast for small fixes)
- **Image analysis** (`meta-llama/llama-4-scout-17b-16e-instruct` vision) — screenshot/mockup/document analysis, requires the Groq key

> **Groq is optional since v5.2:** Without a Groq key, the daemon logs a warning but starts normally. Interview and healing fall back to the codegen provider (DeepSeek, OpenAI, etc.). Image upload is disabled. The Setup Guide now treats Groq as optional.

---

## 10. Budget & Cost Tracking

### Budget Oracle (`internal/budget/oracle.go`)

Thread-safe pre-flight check before every LLM call.

```
CanSpend(estimatedOutputTokens int) bool
  cost = estimatedOutputTokens * $0.28/1M
  return spent + cost <= lifetimeCap && runSpent + cost <= perRunCap
```

Defaults:
- `lifetime_cap`: $2.00
- `per_run_cap`: $0.25

Persisted to `~/.pragma/budget.json` after every `Record()` call (atomic write via temp file + rename).

### Token Ledger (`internal/budget/ledger.go`)

Records actual spend after each phase and project. Rotates at 1,000 phase entries / 200 project entries to prevent unbounded growth.

```json
{
  "phases": [{"phase": "spec_compilation", "cost": 0.018}],
  "projects": [{"run_id": "run-...", "project_name": "...", "total_cost": 0.031}],
  "lifetime_cost": 0.031,
  "rolling_average": 0.031
}
```

Exposed via `GET /api/budget`.

---

## 11. Checkpointing & Resume

### What is saved

`RunState` is serialized to `<output_dir>/<run_id>/checkpoint.json` after:
- Research completion
- Spec compilation
- Start of each generation slice
- Run completion

```go
type RunState struct {
    RunID          string
    Phase          Phase
    ProjectName    string
    ProfileName    string
    Manifest       json.RawMessage
    Research       json.RawMessage
    Spec           json.RawMessage
    SliceIndex     int
    FilesCompleted []string
    FilesRemaining []string
    FilesFailed    []string
    CostSoFar      float64
}
```

### Resume behavior

`pragma` → Sidebar → click a paused run → `resume_run` WebSocket action.

The pipeline:
1. Loads checkpoint from disk
2. Re-plans slices from the saved spec
3. Skips files already in `FilesCompleted`
4. Regenerates only remaining files
5. Runs the full post-generation suite

### Reconnection

If the browser disconnects mid-run (tab closed, network drop), reconnecting to `localhost:3777` triggers `syncServerState()` which calls `GET /api/status`. The response includes `files_completed_list` (array of paths), `phase`, `project_name`, and `total_files`. The frontend restores its state from this.

---

## 12. Conformance & Healing

### Conformance Check

After each file is generated, `check_conformance(content, language, rules)` runs:

- **Python**: checks `ban_print_statements`, `require_type_hints`, `ban_mutable_defaults`, `require_docstrings` (controlled by profile's `[conformance_rules]`)
- **TypeScript**: checks `ban_any_type`, `require_named_exports`, `require_strict_tsconfig`, `ban_console_log`
- **Go**: checks `require_error_handling`, `ban_panic`, `require_context_param`

Returns a list of `Violation(line, rule, message)` objects.

### Healing Loop

If violations are found:

1. `groq.heal_code(messages)` using `openai/gpt-oss-20b` (1,000 t/s, free)

The healer receives: original code, violation list, and file contract. It outputs only the fixed source code (no markdown, no explanation).

After healing, `strip_code_fences()` removes any accidental markdown fences from the output.

### tree-sitter AST Parsing

Used for:
- Conformance checking (function/class extraction)
- Static analysis (import resolution)
- Interface extraction for dependency context sharding

Supported grammars: Python, JavaScript, TypeScript, Go (via `tree-sitter-python`, `tree-sitter-javascript`, `tree-sitter-go`).

---

## 13. Security Model

### API Key Storage

Keys are stored in the OS keyring (macOS Keychain, Linux libsecret/D-Bus, Windows DPAPI) via `github.com/zalando/go-keyring`.

**Linux/WSL fallback:** On Linux, the keyring is frequently unavailable (no D-Bus in WSL, Docker, CI). The keyring write is always mirrored to `~/.pragma/credentials.json` with `0600` permissions. Reads check keyring first, then file.

Key names: `deepseek`, `groq`, `custom`.

Keys are injected into the daemon's environment as `DEEPSEEK_API_KEY`, `GROQ_API_KEY`, etc. They are never written to disk in plaintext beyond `credentials.json`.

### Request Security

- All POST endpoints reject requests with a non-localhost `Origin` header (CORS middleware)
- WebSocket connections are rate-limited to 10 messages/second per client
- WebSocket `Origin` header must be localhost or absent
- Log output passes through a redaction filter (`sk-[A-Za-z0-9]+` → `[REDACTED]`)

### Test Command Execution

The spec's `setup.test` field is executed via `sh -c`. Before execution:
1. Command must start with an allowlisted prefix (`pytest`, `go test`, `npm test`, etc.)
2. Command must not contain shell metacharacters (`;`, `&&`, `||`, `|`, `` ` ``, `$(`, `>`, `<`)

### Generated Code Security

The security audit phase scans generated files for:
- Hardcoded secrets (API keys, passwords in source)
- Missing password hashing
- Unsanitized user input
- Exposed endpoints without authentication

Results are advisory — they don't block the run but are shown to the user.

---

## 14. WebSocket Protocol

All messages are newline-delimited JSON.

### Server → Client Events

| Type | Fields | Description |
|------|--------|-------------|
| `phase_changed` | `from`, `to` | Pipeline phase transition |
| `interview_response` | `content`, `done`, `manifest` | AI message during ideation |
| `spec_ready` | `spec`, `file_count`, `test_count`, `summary` | Spec compiled |
| `dag_ready` | `slices`, `est_seconds`, `est_cost` | Execution plan ready |
| `file_completed` | `path`, `healed`, `failed`, `duration_ms`, `description` | File generated |
| `budget_updated` | `run_spent`, `remaining` | Cost update |
| `run_complete` | `output_path`, `file_count`, `healed`, `failed`, `cost`, `coverage`, `project_name`, `manifest`, `spec` | Done |
| `security_audit` | `warnings` | Post-gen security findings |
| `test_run` | `command`, `passed`, `output` | Test suite result |
| `runtime_validation_error` | `message`, `logs` | Docker smoke test failed |
| `runtime_validation_passed` | — | Docker smoke test passed |
| `spec_amendment_proposed` | `file_path`, `reason` | Repeated generation failure suggests spec issue |
| `extend_project_ready` | `delta_spec`, `run_id` | Delta spec from extend_project |
| `refine_project` | `manifest`, `spec` | Server signals refine mode available |
| `error` | `message`, `fatal` | Error (fatal=true means run is dead) |
| `log` | `level`, `message` | Internal log (debug panel) |
| `profile_chosen` | `profile` | Stack profile auto-selected |
| `warning` | `message` | Non-fatal warning (e.g., multiple tabs) |

### Client → Server Actions

| Action | Fields | Description |
|--------|--------|-------------|
| `send_message` | `content` | User message during ideation |
| `approve_spec` | — | Approve spec, proceed to DAG review |
| `reject_spec` | — | Reject spec, reset to idle |
| `approve_dag` | — | Approve DAG, start generation |
| `reject_dag` | — | Reject DAG, reset to idle |
| `update_manifest` | `project_name`, `additions` | PreCompileView update before pipeline starts |
| `resume_run` | `run_id` | Resume a checkpointed run |
| `extend_project` | `run_id`, `content` | Add features to an existing run |
| `pause_run` | — | Not yet implemented |

---

## 15. REST API

All endpoints are on `localhost:3777`. POST endpoints reject non-localhost `Origin` headers.

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/ws` | WebSocket | — | Real-time event stream |
| `/api/settings` | GET | — | Current config + key status (masked) |
| `/api/settings` | POST | — | Save API key, mode, profile |
| `/api/validate-key` | POST | — | Test an API key before saving |
| `/api/status` | GET | — | Current run state (phase, files, progress) |
| `/api/runs` | GET | — | Run history (completed + paused) |
| `/api/budget` | GET | — | Budget oracle status + ledger summary |
| `/api/logs` | GET | — | Last 50 lines of daemon.log |
| `/api/profiles` | GET | — | List available build profiles with metadata |
| `/api/health` | GET | — | System health (Python, daemon, keys, Docker, WSL) |
| `/api/approve-spec` | POST | — | Approve spec (alternative to WS action) |
| `/api/approve-dag` | POST | — | Approve DAG (alternative to WS action) |
| `/api/pause` | POST | — | Not yet implemented |
| `/api/resume` | POST | `run_id` | Resume a run (uses 30-min timeout context to prevent resource leaks) |
| `/api/analyze-image` | POST | — | Analyze uploaded image via Groq Scout vision |
| `/api/select-profile` | POST | — | Auto-select build profile from description text |
| `/api/extend-project` | POST | `checkpoint_manifest`, `checkpoint_spec`, `new_requirements` | Impact analysis + delta spec for interactive refinement |
| `/api/apply-delta` | POST | `run_id`, `delta_spec` | Apply approved delta to project files |
| `/api/run-project` | POST | `run_id` | Run docker compose up in project directory |
| `/api/open-folder` | POST | `path` | Open output directory in file manager |
| `/api/download/{run_id}` | GET | — | Download generated project as ZIP |
| `/api/readme` | GET | `run_id` | Get generated README text |

### POST /api/settings

```json
{
  "provider": "deepseek | groq | custom",
  "api_key": "sk-...",
  "groq_api_key": "gsk_...",
  "base_url": "https://api.deepseek.com",
  "mode": "fast",
  "profile": "fastapi-async"
}
```

All fields optional. Only non-empty fields are applied. After saving a key, the daemon is started lazily if it wasn't already running.

---

## 16. Configuration

File: `~/.pragma/config.toml` (created with defaults on first run).

```toml
mode    = "fast"           # DeepSeek direct API (the only supported provider)
profile = "fastapi-async"  # Default stack profile

[budget]
lifetime_cap = 2.00   # Hard cap on total DeepSeek spend ($)
per_run_cap  = 0.25   # Cap per project ($)

[output]
directory = "./output"  # Expanded to absolute path on load
git_init  = true        # Auto-init git repo in output directory

[daemon]
python_executable = "python3"  # Override if using a venv
```

**Environment variable overrides** (take precedence over config.toml):

| Variable | Overrides |
|----------|-----------|
| `PRAGMA_MODE` | `mode` |
| `PRAGMA_OUTPUT` | `output.directory` |
| `PRAGMA_PROFILE` | `profile` |
| `PRAGMA_PORT` | HTTP server port (default: 3777) |
| `DEEPSEEK_API_KEY` | DeepSeek key (skips keyring) |
| `GROQ_API_KEY` | Groq key (skips keyring) |

**Relative paths:** `output.directory` is expanded to absolute using the working directory at startup. Running `pragma` from different directories always produces output in the same place.

---

## 17. Stack Profiles

Profiles are TOML files embedded in the binary at `internal/config/profiles/*.toml`. They control how the spec compiler and code generator behave for a given stack.

### Available Profiles

| Profile ID | Language | Framework | Database | Notes |
|------------|----------|-----------|----------|-------|
| `fastapi-async` | Python 3.12 | FastAPI | PostgreSQL + SQLAlchemy 2.0 | Async, Alembic migrations |
| `express-drizzle` | TypeScript | Express 5 | PostgreSQL + Drizzle ORM | Type-safe SQL |
| `express-prisma` | TypeScript | Express 5 | PostgreSQL + Prisma | Schema-first ORM |
| `hono-drizzle` | TypeScript | Hono | PostgreSQL + Drizzle ORM | Edge-ready |
| `nextjs-app` | TypeScript | Next.js 15 | PostgreSQL + Drizzle ORM | App Router, server components |
| `fiber-sqlc` | Go | Fiber v3 | PostgreSQL + sqlc + pgx | Raw SQL, type-safe |

### Profile Structure

Each profile TOML contains:

```toml
[meta]
name     = "FastAPI Async"
language = "python"
version  = "3.12"

[framework]
name    = "fastapi"
version = "0.115.x"

[patterns]
context = """
Use async/await throughout. Dependency injection via Depends().
Repository pattern for database access. Pydantic v2 for validation.
"""

[engineering]
context = """
Type hints on all functions. No mutable default arguments.
Use pathlib.Path, not os.path. Prefer dataclasses over dicts for structured data.
"""

[security]
context = """
Hash passwords with bcrypt. Never store plaintext. JWT with short expiry.
Parameterized queries only. Validate all user input with Pydantic.
"""

[conformance_rules]
ban_print_statements  = true
require_type_hints    = true
ban_mutable_defaults  = true
```

The `[patterns]`, `[engineering]`, and `[security]` context blocks are injected directly into the spec compiler system prompt.

---

## 18. Image Ingest (Groq Scout Vision)

Pragma supports an **optional** image input path that extracts a manifest fragment from a screenshot, document, or architecture diagram. This path is additive — it pre-fills the interview description and feeds into the standard pipeline unchanged.

### Architecture

```
User uploads image (UI)
        |
        v
POST /api/analyze-image (Go handler)
        |
        v
daemon.analyze_image() RPC
        |
        v
GroqClient.vision_chat()  <-- ONLY Groq, NEVER DeepSeek
model: meta-llama/llama-4-scout-17b-16e-instruct
        |
        v
JSON manifest fragment
{description, endpoints, data_models, integrations}
        |
        v
Pre-fill ProjectInput textarea
        |
        v
Normal interview + spec + codegen pipeline (unchanged)
```

### Constraints

| Constraint | Value | Reason |
|---|---|---|
| Model | `meta-llama/llama-4-scout-17b-16e-instruct` | Only vision model on Groq |
| Provider | Groq only | DeepSeek API is text-only |
| Max image size | 4 MB | Groq API limit |
| Supported formats | JPEG, PNG, WebP | Standard web image formats |
| Groq key required | Yes | Feature degrades gracefully if absent |
| DeepSeek images | Never | DeepSeek does not support image inputs |
| OCR pipeline (Tesseract) | Not in v1 | Scout handles well enough for most docs |
| Self-hosted VLMs | Not in v1 | Conflicts with single-binary distribution |

### Modes

| Mode | `mode` value | Prompt strategy |
|---|---|---|
| UI screenshot / mockup | `ui` | Extract app description, API endpoints, data models from the UI |
| Requirements document | `document` | Extract spec items, entities, integrations from written spec |
| Architecture / ER diagram | `diagram` | Extract entities, relationships, services from system diagram |

### REST Endpoint

```
POST /api/analyze-image
Content-Type: application/json

{
  "image_base64": "<base64-encoded image, no data: URI prefix>",
  "mode": "ui" | "document" | "diagram"
}

Response 200:
{
  "description": "Plain English app description extracted from the image",
  "endpoints": ["POST /api/users", "GET /api/items"],
  "data_models": ["User: id, name, email", "Item: id, title, price"],
  "integrations": ["Stripe"],
  "tokens_used": 312,
  "model": "meta-llama/llama-4-scout-17b-16e-instruct"
}

Response 400: {"error": "Groq API key required..."}   -- Groq not configured
Response 400: {"error": "image too large (max 4 MB)"}
Response 503: {"error": "daemon not running"}
```

### Budget Tracking

Groq Scout token costs (~$0.11/M input, $0.34/M output) are tracked in the ledger under the `vision_<mode>` entry. This is separate from the main pipeline DeepSeek budget tracked by the oracle.

### Frontend Integration

1. User selects a file in `ProjectInput.svelte` (file input, max 4 MB)
2. File is read as base64 via `FileReader`
3. POST to `/api/analyze-image`
4. On success: `description` field pre-fills the textarea; `endpoints` and `data_models` are stored as structured JSON in the backend's `interviewState.analysisResult`
5. User edits description if needed, then clicks "Start Building" as normal
6. When the pipeline starts, `interviewState.analysisResult` is merged into the manifest (`endpoints`, `data_models`, `integrations`) so the AI has structured data from the image, not just description text
7. The stack is auto-selected from the description via `POST /api/select-profile` (no manual picker); profile shown as read-only in `PreCompileView`

---

## 19. Interactive Refinement

After a project is generated, Pragma offers an interactive "chat-to-refine" capability that allows users to request changes, additions, or fixes through natural conversation — turning Pragma from a one-time generator into an ongoing AI pair programmer.

### 19.1 Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    RefineView.svelte                       │
│              (Chat UI, Impact Display, Approve/Cancel)    │
└──────────────────────┬───────────────────────────────────┘
                       │ POST /api/extend-project
                       │ POST /api/apply-delta
┌──────────────────────▼───────────────────────────────────┐
│                    GO SERVER                               │
│  ┌──────────────────────────────────────────────────┐     │
│  │  handleExtendProjectHTTP →                             │
│  │  handleApplyDelta     →                                │
│  │  handleExtendProject  →  WebSocket integration         │
│  └──────────────────────────────────────────────────┘     │
└──────────────────────┬───────────────────────────────────┘
                       │ JSON-RPC 2.0
┌──────────────────────▼───────────────────────────────────┐
│                  PYTHON DAEMON                             │
│  ┌──────────────────────────────────────────────────┐    │
│  │  extend_project() → Impact Analyzer + Delta Spec  │    │
│  │  apply_delta()    → LLM-guided intelligent merge  │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
```

### 19.2 Two-Phase Extend Process

**Phase 1 — Impact Analyzer (explicit, blocking):**

Before any code is changed, Pragma analyzes the request's scope:

```
POST /api/extend-project
{
  "checkpoint_manifest": {...},  // original project manifest
  "checkpoint_spec": {...},      // full spec from the original run
  "new_requirements": "Add a dark mode toggle to the settings"
}

Response 200:
{
  "impact": {
    "impact_summary": "Adding a toggle that switches between light and dark themes across all pages",
    "affected_files": ["frontend/src/Settings.tsx", "frontend/src/App.tsx"],
    "new_files": ["frontend/src/DarkModeToggle.tsx", "frontend/src/theme.css"],
    "risk_level": "medium",
    "risk_reasons": ["Existing CSS uses hardcoded colors that need to be replaced with CSS variables"]
  },
  "delta": {
    "files": [
      {"path": "frontend/src/DarkModeToggle.tsx", "role": "component", "depends_on": [...]}
    ],
    "dependencies": ["react-dark-mode-toggle@3.5.0"]
  }
}
```

The **impact** object is presented to the user in plain English. Risk levels: `low` (🟢), `medium` (🟡), `high` (🔴). The user must explicitly approve before any changes are applied.

**Phase 2 — Delta Application (user-approved):**

```
POST /api/apply-delta
{
  "run_id": "run-abc123",
  "delta_spec": { "files": [...] }
}

Response 200:
{
  "success": true,
  "updated_spec": {...}
}
```

### 19.3 Intelligent Merging (apply_delta RPC)

The `apply_delta` RPC (`daemon/pragma_daemon/methods.py`) performs LLM-guided intelligent merging rather than simple file writes:

- **New files**: Generated via `generate_file()` — full contract-based codegen
- **Existing files**: The LLM receives the current file content plus the delta contract. It produces a merged version that preserves all existing functionality while adding the requested changes. This prevents overwriting customizations the user may have made
- **Path traversal protection**: All file paths are validated to stay within the output directory
- **Error accumulation**: Individual file failures don't block other files; errors are collected and reported

**Fallback path (Go):** If the `apply_delta` RPC is unavailable (old daemon), the Go server falls back to writing files directly from the delta spec, with full path traversal validation.

### 19.4 Frontend (RefineView.svelte)

The refine UI is a chat interface accessible via the "Refine This Project" button on `CompleteView`. Features:

- **Conversation view**: Messages from both user and Pragma with role-labeled bubbles
- **Impact card**: When the impact analyzer returns, it's shown as a structured card with affected files, new files, risk level, and risk reasons — all in plain English
- **Approve/Cancel**: Two explicit buttons; user must approve before code changes
- **Loading states**: Both analysis and application phases show spinners with "Analyzing impact..." / "Applying changes..." text
- **XSS protection**: User and AI content is sanitized (`sanitizeHtml()`) before rendering — strips `<script>`, `<img>`, `<iframe>`, `on*` handlers, `javascript:` URIs
- **Accessibility**: `aria-live="polite"` on conversation area, `aria-label` on buttons, `role="log"` on conversation history
- **Store integration**: `checkpointManifest` and `checkpointSpec` stores are auto-populated from the `run_complete` WebSocket event (which now includes `manifest` and `spec` fields)

### 19.5 WebSocket Integration

The `extend_project` WebSocket action (backward-compatible) uses the same two-phase RPC. Results are broadcast as:

```json
{
  "type": "extend_project_ready",
  "delta_spec": { "impact": {...}, "delta": {...} },
  "run_id": "run-abc123"
}
```

### 19.6 Phase State

A new `'refine'` phase is added to the pipeline state machine:

| Phase | Wire name | Description |
|-------|-----------|-------------|
| Refine | `refine` | User is chatting with Pragma to extend/modify their project |

The refine phase is NOT linear — users can toggle between `complete` and `refine` phases freely.

---

## 20. Runtime Smoke Test Agent

After code generation, Pragma can verify that the generated application actually starts — catching runtime errors (missing environment variables, port conflicts, database connection failures) that static analysis cannot detect.

### 20.1 Architecture

```
finishRun() in pipeline/service.go
        |
        v
Check for docker-compose.yml existence
        |
        v
docker compose up -d --build  (3 min timeout)
        |
    ┌───┴───┐
    │       │
 Success   Failed
    │       │
    │       └──→ Emit warning, run continues
    │
    v
Poll container states every 3s (up to 30s)
    |
    ├── All containers reach "Up" state
    │       │
    │       └──→ Emit RuntimeValidationPassedEvent
    │
    └── Timeout (container didn't start in 30s)
            │
            └──→ Fetch docker compose logs (last 100 lines)
                 Emit RuntimeValidationErrorEvent with logs
                 |
                 v
           docker compose down  (cleanup)
```

### 20.2 Implementation Details

**Location:** `internal/pipeline/service.go`, within `finishRun()`, after test execution and before the final `RunCompleteEvent`.

**Container state detection:** Uses `docker compose ps --format json` and parses the JSON output to check each service's `Status` field. Only `"Up"` (or strings starting with `"Up"`) are considered healthy. This replaces the earlier fragile string-matching approach that could false-positive on container names containing "exit" or "dead".

**Timeout chain:**
| Operation | Timeout | Reason |
|-----------|---------|--------|
| `docker compose up --build` | 3 minutes | First build may be slow (downloading images) |
| Healthcheck polling | 30 seconds | After successful up, wait for app to start |
| `docker compose logs` | Default (30s) | Quick log fetch |
| `docker compose down` | Default (30s) | Cleanup |

**Non-blocking:** The smoke test is non-fatal. If Docker is not installed, `docker compose up` fails with a logged warning and the run completes normally.

**Cleanup:** After the smoke test (whether passed or failed), `docker compose down` removes containers and networks. Named volumes persist (by design — the safer default).

### 20.3 Events

**RuntimeValidationPassedEvent:**
```go
type RuntimeValidationPassedEvent struct{}
```

**RuntimeValidationErrorEvent:**
```go
type RuntimeValidationErrorEvent struct {
    Message string  // "Application failed to start within 30 seconds"
    Logs    string  // Last 100 lines of docker compose logs (truncated to 2000 chars)
}
```

### 20.4 Frontend Display

The `CompleteView` component checks the `runtimeValidationError` store after a run completes:
- **No event or passed**: No display (success is silent)
- **Error**: Shows a yellow warning card with "Quick health check found issues", the error message, and a collapsible `<details>` section showing the Docker logs

### 20.5 Self-Healing Integration

The `fix_runtime_error` RPC (`daemon/pragma_daemon/methods.py`) is available for automated healing from runtime errors:

```
RPC: fix_runtime_error(error_logs, file_contract, current_content, profile) -> dict

Returns:
{
  "content": "<fixed source code>",
  "usage": { "input_tokens": ..., "output_tokens": ... }
}
```

The healing agent:
1. Receives the runtime error logs, the original file contract, and the generated file content
2. Identifies the root cause (missing import, syntax error, wrong variable name, missing env var)
3. Outputs ONLY the corrected source code (no markdown fences, no explanation)
4. Prefers Groq (fast healing, 1,000 t/s) but falls back to DeepSeek

This is currently available as a standalone RPC. Full automated integration (auto-heal loop on smoke test failure) is planned for a future release.

---

## 21. Role-Specialized Spec Compilation

The 3-pass spec compiler has been enhanced with role-specialized system prompts that emulate a virtual "software company" structure — similar to MetaGPT and ChatDev, but achieved entirely through prompt engineering without complex multi-agent frameworks.

### 21.1 Pass Roles

**Pass 1 — Lead Architect (thinking ON, medium effort):**
```
ROLE: You are the Lead Software Architect.
Focus on: correct data models, comprehensive API routes,
proper dependency injection, and logical file structure.
Do not worry about minor formatting yet; focus on architectural completeness.
```

**Pass 2 — Security & Database Expert (thinking OFF):**
```
ROLE: You are the Security & Database Expert.
Focus exclusively on: missing authentication/authorization checks,
SQL injection risks, missing database indexes, improper error handling,
and ensuring all local imports resolve.
Fix any architectural gaps you find.
```

**Pass 3 — Finalizer (thinking OFF, conditional):**
```
ROLE: You are the Finalizer.
Focus on: consistent naming conventions, valid JSON structure,
resolving all depends_on links, and removing any redundancy.
Output ONLY the final, valid JSON object.
```

### 21.2 Implementation

- **Location:** `daemon/pragma_daemon/spec_compiler.py`, `_build_pass1_messages()`, `_build_pass2_messages()`, `_build_pass3_messages()`
- **Backward compatible:** The core 3-pass pipeline logic is unchanged. Only the system messages are enhanced with role prefixes
- **Cost impact:** Same 3 LLM calls; the role prefix adds ~50 tokens per pass (~$0.000007)
- **Chained compilation:** Large projects (>18 endpoints+models) compile in domain modules (core, models, services, routes, tests) — each module receives the role-appropriate system prompt

---

## 22. Golden Template System

To prevent structural hallucinations and ensure every generated project matches framework best practices, Pragma's stack profiles now include verified "golden template" snippets.

### 22.1 Template Types

Each profile can optionally include a `[templates]` section with minimal, verified, working examples:

| Template Key | Purpose |
|-------------|---------|
| `route` | Minimal FastAPI route with Depends(), HTTPException, and proper schema usage |
| `model` | Correct SQLAlchemy 2.0 model using Mapped[] type annotations (never Column()) |
| `docker_compose` | Verified docker-compose.yml matching the profile's database and framework |

### 22.2 Template Enforcement

The golden templates are injected into the spec compiler's system prompt as part of the framework patterns block. The compiler is instructed to use these snippets as the **exact structural blueprint** — the generated code must conform to the same patterns.

### 22.3 Example: FastAPI Async Golden Templates

**Route template** (from `fastapi-async.toml`):
```python
from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession
from app.core.database import get_db
from app.schemas.item import ItemCreate, ItemResponse
from app.services.item_service import create_item

router = APIRouter(prefix="/items", tags=["items"])

@router.post("/", response_model=ItemResponse, status_code=status.HTTP_201_CREATED)
async def create_item_endpoint(item: ItemCreate, db: AsyncSession = Depends(get_db)):
    try:
        return await create_item(db, item)
    except ValueError as e:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(e))
```

**Model template** (from `fastapi-async.toml`):
```python
from sqlalchemy import String
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column
from typing import Optional

class Base(DeclarativeBase):
    pass

class Item(Base):
    __tablename__ = "items"
    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    created_at: Mapped[Optional[datetime]] = mapped_column(default=datetime.utcnow)
```

---

## 23. CLI Reference

```
pragma [flags] [command]

Commands:
  setup     Install the Python daemon (venv + dependencies) interactively
  doctor    Check system health (Python, API keys, disk space)
  upgrade   Download and replace binary from GitHub Releases (SHA256 verified)
  publish   Initialize git repo in output dir + print GitHub push instructions
  clean     Remove old run directories (keeps 5 most recent)

Flags:
  --tui           Run in Bubble Tea terminal UI instead of web browser
  --headless      Read manifest from stdin, emit events to stdout (CI mode)
  --budget FLOAT  Override per-run budget cap for this invocation
  --version       Print version and exit
```

### Headless mode

```bash
echo '{"description": "A REST API for task management", "endpoints": [...]}' | pragma --headless
```

Reads a manifest JSON from stdin, runs the full pipeline (skipping ideation), and prints events to stdout. Useful for CI/CD pipelines.

### TUI mode

Classic Bubble Tea terminal UI. Same pipeline, different interface. Useful in environments without a browser (SSH sessions, headless servers).

---

## 24. Folder Structure

```
pragma/
├── cmd/
│   └── pragma/
│       ├── main.go          # Entry point, flag parsing, mode dispatch
│       ├── doctor.go        # pragma doctor command
│       ├── upgrade.go       # pragma upgrade command (SHA256 verified)
│       ├── publish.go       # pragma publish command
│       └── clean.go         # pragma clean command
│
├── internal/
│   ├── budget/
│   │   ├── oracle.go        # Pre-flight cost checks, lifetime/run caps
│   │   └── ledger.go        # Per-phase and per-project cost recording
│   ├── config/
│   │   ├── config.go        # TOML loading, env overrides, path expansion
│   │   ├── profiles.go      # Embedded profile loading
│   │   └── profiles/        # Stack profile TOML files
│   ├── daemon/
│   │   ├── lifecycle.go     # Spawn, PID file, stale socket cleanup
│   │   ├── client.go        # JSON-RPC client (multiplexed by request ID)
│   │   └── health.go        # Ping loop, restart on failure
│   ├── keyvault/
│   │   └── keyring.go       # OS keyring + file fallback (0600)
│   ├── pipeline/
│   │   ├── service.go       # Main pipeline orchestrator
│   │   ├── state.go         # RunState type, Phase enum
│   │   ├── events.go        # Event types for WebSocket broadcast
│   │   └── checkpoint.go    # Save/load RunState to disk
│   ├── server/
│   │   ├── server.go        # HTTP server, CORS middleware, route registration
│   │   ├── hub.go           # WebSocket hub, rate limiting, event serialization
│   │   ├── handlers.go      # REST endpoint handlers
│   │   ├── actions.go       # WebSocket action dispatch
│   │   ├── interview.go     # Ideation phase, manifest gate
│   │   ├── validate.go      # API key validation with SSRF protection
│   │   └── preview.go       # Live preview dev server (see §24)
│   └── tui/
│       └── *.go             # Bubble Tea screens (home, interview, generating, etc.)
│
├── daemon/
│   └── pragma_daemon/
│       ├── main.py          # Entry point, RPC server setup, key validation
│       ├── rpc.py           # asyncio JSON-RPC server with semaphore
│       ├── methods.py       # All RPC method implementations
│       ├── deepseek.py      # DeepSeek client, model discovery, retry
│       ├── groq_client.py   # Groq client (interview, healing)
│       ├── spec_compiler.py # 3-pass spec compilation, chained compilation
│       ├── spec_validator.py# Structural validation (cycles, duplicates)
│       ├── code_generator.py# Per-file generation, README generation
│       ├── conformance.py   # tree-sitter conformance checks, healing
│       ├── research.py      # DeepWiki MCP + DuckDuckGo
│       └── cache.py         # L1 persistent cache (JSON, keyed by prompt hash)
│
├── web/
│   ├── src/
│   │   ├── routes/
│   │   │   └── +page.svelte # Main page, phase routing
│   │   └── lib/
│   │       ├── stores/
│   │       │   ├── ws.ts    # WebSocket store, all pipeline state
│   │       │   └── refine.ts     # Refinement chat stores
│   │       ├── components/  # UI components (one per phase/feature)
│   │       │   ├── ChatView.svelte
│   │       │   ├── CompleteView.svelte
│   │       │   ├── RefineView.svelte     # Interactive refinement chat (see §19)
│   │       │   ├── GeneratingView.svelte
│   │       │   ├── SpecReview.svelte
│   │       │   ├── DagApproval.svelte
│   │       │   ├── PreCompileView.svelte
│   │       │   ├── WorkingView.svelte
│   │       │   ├── ProjectInput.svelte
│   │       │   ├── SetupGuide.svelte
│   │       │   ├── Settings.svelte
│   │       │   ├── Sidebar.svelte
│   │       │   ├── DisconnectedBanner.svelte
│   │       │   └── StatusBar.svelte
│   └── embed.go             # go:embed directive for web/build/
│
├── spec.md                  # This document
├── README.md                # User-facing documentation
├── go.mod
├── install.sh               # Linux/macOS/WSL installer (uv-based)
└── install.ps1              # Windows PowerShell installer
```

---

## 25. Live Preview / Sandbox

After generation, Pragma can start a dev server for the generated project and show a live preview inside the CompleteView.

### 25.1 Architecture

```
CompleteView.svelte → "Live Preview" button
        │
        ▼
POST /api/preview/start { run_id }
        │
        ▼
preview.go (Go server)
   ├── detectAndStartDevServer(runDir)
   │   ├── package.json       → npm run dev
   │   ├── requirements.txt   → python3 main.py
   │   └── docker-compose.yml → docker compose up -d
   │
   └── Returns preview URL
        │
        ▼
CompleteView embeds <iframe> pointing to the preview URL
```

### 25.2 Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/preview/start` | POST | Start dev server, return URL |
| `/api/preview/stop` | POST | Stop running preview |
| `/api/preview/status` | GET | Current preview state |
| `/api/preview/proxy/` | GET | Proxy requests to dev server |

### 25.3 Implementation

**File:** `internal/server/preview.go`

- Only one preview can run at a time (mutex-guarded `activePreview` state)
- Ports are auto-allocated starting from 4000
- For Node.js projects: auto-runs `npm install` if `node_modules` is missing
- The iframe uses `sandbox="allow-scripts allow-same-origin allow-forms"` for security
- Preview stops when user clicks "Stop" or starts a new preview

### 25.4 Project Type Detection

| File found | Command | Port |
|------------|---------|------|
| `package.json` | `npm run dev -- --port <port>` | 4000+ |
| `requirements.txt` + `main.py`/`app.py` | `python3 main.py` | 4000+ |
| `docker-compose.yml`/`compose.yml` | `docker compose up -d --build` | 4000+ |

---

## 26. Iterative Chat During Generation

Users can send messages during the generation phase. These are queued and automatically applied as post-generation refinements.

### 26.1 Architecture

```
User types message during generation
        │
        ▼
dispatchAction (actions.go)
        │
        ├── If phase == PhaseGenerating → service.QueueMessage(msg)
        │   └── Broadcast "Message queued" to frontend
        │
        └── Otherwise → normal interview flow
```

After generation completes, queued messages are available via `DrainQueuedMessages()` and can be applied using the standard `extend_project` → `apply_delta` pipeline.

### 26.2 Implementation

**Pipeline service additions** (`internal/pipeline/service.go`):
```go
type Service struct {
    // ...
    QueuedMessages []string
    queueMu        sync.Mutex
}

func (s *Service) QueueMessage(msg string)
func (s *Service) DrainQueuedMessages() []string
func (s *Service) HasQueuedMessages() bool
```

**Frontend** (`GeneratingView.svelte`):
- Chat textarea with "Queue" button at the bottom of the generation view
- Shows "N messages queued" counter in the stats row
- Messages are sent via the normal `sendMessage()` WebSocket action

---

## 27. Spec Progress Streaming

The spec compilation phase now emits progress events so the frontend can show live status rather than a static spinner.

### 27.1 Events

```go
type SpecProgressEvent struct {
    Pass    int    // 1, 2, or 3
    Status  string // "started", "completed", "error"
    Message string // Human-readable status
}
```

### 27.2 Frontend

`WorkingView.svelte` subscribes to the `specProgress` store and shows the current pass message:
- "Drafting spec (Pass 1/3 — Lead Architect)..."
- "Reviewing for security (Pass 2/3)..."
- "Spec compiled successfully"

---

## 28. Concurrent Run Prevention

The pipeline service now prevents multiple runs from starting simultaneously.

```go
func (s *Service) StartRun(ctx context.Context, manifest string, profileName string) error {
    if !s.runLock.TryLock() {
        return fmt.Errorf("a run is already in progress")
    }
    defer s.runLock.Unlock()
    // ...
}
```

---

## 29. Checkpoint Recovery & Validation

Checkpoints are now validated before loading, with automatic recovery from backup.

**Backup strategy:** Before overwriting `checkpoint.json`, the previous version is copied to `checkpoint.json.bak`.

**Validation on load:**
- JSON must parse successfully
- `RunID` field must be non-empty
- `Phase` field must be non-zero

If the primary checkpoint is corrupted, the `.bak` file is attempted. If that also fails, a clear error is returned.

---

## 30. Budget Exhaustion UX

When a file is skipped due to budget constraints, the error message now includes precise details:

```
"Budget exceeded for src/routes/auth.py. Spent: $0.18 / $0.25 cap. Need ~$0.04 more.
Increase your per-run budget in Settings or use --budget flag."
```

A non-fatal `ErrorEvent` is emitted so the frontend can display this to the user without aborting the entire run.

---

## 31. BYOK OpenAI-Compatible Client

When the user configures a non-DeepSeek provider, Pragma uses `OpenAICompatClient` (`daemon/pragma_daemon/openai_compat.py`) instead of `DeepSeekClient`.

### 31.1 Supported Providers

| Provider | Auto-detect | Thinking |
|----------|-------------|----------|
| OpenAI | ✅ Models endpoint | ✅ (o1, o3) |
| OpenRouter | ✅ Models endpoint | ❌ |
| Together AI | ✅ Models endpoint | ❌ |
| Ollama (local) | ✅ /api/tags | ❌ |
| Anthropic | ✅ Models endpoint | ❌ |
| Any OpenAI-compatible | Manual model config | User-configurable |

### 31.2 Model Discovery

The client attempts to call `GET /models` to discover available models. If that fails (Ollama), it falls back to `GET /api/tags`. User-configured model names in config.toml take precedence over auto-discovery.

### 31.3 Pricing & Budget

Since Pragma cannot know the pricing of arbitrary providers, the budget oracle uses DeepSeek V4-Flash pricing as a conservative estimate. Users with more expensive providers should set `lifetime_cap` and `per_run_cap` accordingly in `config.toml`.

---

## 32. Known Limitations

1. **No deployment.** Pragma generates Dockerfiles and docker-compose configs. Actual deployment to Railway, Fly.io, etc. is manual.

2. **English only.** All prompts, generated comments, and documentation are in English.

3. **Single language per project.** Each project uses one primary language. Polyglot projects (e.g., Go backend + React frontend) are not supported in a single run.

4. **Spec amendment is fire-and-forget.** When repeated file failures suggest a spec issue, a `spec_amendment_proposed` event is emitted but the pipeline does not pause. Full blocking amendment with user approval is a planned feature.

5. **Pause is not implemented.** The `pause_run` action is accepted but does nothing. The pipeline runs to completion or failure.

6. **pragma publish** creates a local git repo and prints GitHub push instructions. It does not push automatically (no GitHub OAuth).

7. **Test execution requires Docker or local toolchain.** The test runner executes `spec.setup.test` in the output directory. If the required runtime (Python, Node, Go) is not installed, tests will fail.

8. **Docker smoke test healthcheck uses polling.** The smoke test polls container states every 3 seconds for up to 30 seconds. Applications with very slow startup (some Next.js SSR pages may take 60s+) may be incorrectly flagged as failed. The timeout is not yet configurable per profile.

9. **Refinement requires original checkpoint data.** The `checkpointManifest` and `checkpointSpec` stores are populated from the `run_complete` WebSocket event. If the browser was closed before the run completed, these stores will be empty and the Refine feature will not work for that run.
