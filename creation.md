# Pragma: Build Contract

**This document is Pragma's own Build Contract — the same standard Pragma produces for its users, applied to itself.**

Every file, function, import, dependency, and test is declared here. A developer (or a future version of Pragma) should be able to implement this project by following this contract alone — no architectural decisions, no ambiguity.

---

## Setup

```toml
[go]
version = "1.22"
module = "github.com/sarv-projects/pragma"
build = "go build -o pragma ./cmd/pragma"
test = "go test ./..."
lint = "golangci-lint run"

[python]
version = "3.12"
venv = "cd daemon && python -m venv .venv && source .venv/bin/activate"
install = "cd daemon && pip install -e '.[dev]'"
test = "cd daemon && pytest"
lint = "cd daemon && ruff check ."

[run]
dev = "go run ./cmd/pragma"
build = "go build -o pragma ./cmd/pragma && cd daemon && pip install -e ."
```

---

## Architecture

```
pattern: "Two-process hybrid (Go orchestrator + Python daemon) over Unix domain socket"

principles:
  - Go owns all state, all display, all user interaction
  - Python owns all LLM calls, all AST parsing, all research
  - Communication is JSON-RPC 2.0 over UDS (newline-delimited)
  - Budget enforcement happens in Go BEFORE any call crosses the wire
  - Conformance checking happens in Python AFTER each file is generated
  - The TUI is the product — no CLI subcommand trees

data_flow:
  User → TUI → Pipeline State Machine → JSON-RPC → Python Daemon → DeepSeek API
  DeepSeek API → Python Daemon → JSON-RPC → Pipeline → TUI → User

concurrency:
  Go: main goroutine (TUI), pipeline goroutine, health monitor goroutine
  Python: asyncio event loop, semaphore(20) for concurrent DeepSeek calls
```

---

## Dependencies

### Go (go.mod)

```
github.com/charmbracelet/bubbletea    v1.3+     TUI framework
github.com/charmbracelet/bubbles      v0.20+    TUI components (textinput, viewport, spinner, progress)
github.com/charmbracelet/lipgloss     v1.0+     TUI styling
github.com/zalando/go-keyring         v0.2+     OS keyring access
github.com/pelletier/go-toml/v2       v2.2+     TOML config parsing
```

### Python (pyproject.toml)

```
[project]
dependencies = [
    "httpx>=0.27",              # DeepSeek API + DeepWiki MCP calls
    "duckduckgo-search>=6.0",   # DDG text search fallback
    "tree-sitter>=0.23",        # AST parsing for conformance
    "tree-sitter-python>=0.23", # Python grammar
    "tree-sitter-javascript>=0.23",  # JS/TS grammar
    "tree-sitter-go>=0.23",     # Go grammar
]

[project.optional-dependencies]
dev = [
    "pytest>=8.0",
    "pytest-asyncio>=0.24",
    "ruff>=0.8",
]
```

---


## Files — Go Side

---

### `cmd/pragma/main.go`

```
role: entrypoint
imports: [os, fmt, internal/config, internal/tui]
exports: [main]
```

```go
// public_api:
func main()
  // 1. Parse minimal flags: --headless, --help, --version, doctor
  // 2. If "doctor" subcommand: run doctor(), os.Exit()
  // 3. Load config from ~/.pragma/config.toml
  // 4. Launch TUI via tui.Run(cfg)

depends_on: []
```

---

### `cmd/pragma/doctor.go`

```
role: health_check_command
imports: [fmt, os, net/http, os/exec, internal/config, internal/keyvault]
exports: [runDoctor]
```

```go
// public_api:
func runDoctor() int
  // Checks:
  //   1. Python 3.11+ available (exec: python3 --version)
  //   2. DeepSeek API key present (keyvault or env)
  //   3. DeepSeek API reachable (HEAD https://api.deepseek.com)
  //   4. DeepWiki reachable (HEAD https://mcp.deepwiki.com)
  //   5. Disk space > 100MB free in output dir
  //   6. Terminal width >= 60 columns
  // Returns: 0 if all pass, 1 if any fail
  // Output: table of PASS/FAIL per check

depends_on: [internal/config, internal/keyvault]
```

---

### `internal/budget/oracle.go`

```
role: budget_enforcement
imports: [sync, encoding/json, os, fmt]
exports: [Oracle, Status, New]
```

```go
// public_api:

type Status struct {
    LifetimeCap  float64
    PerRunCap    float64
    TotalSpent   float64
    RunSpent     float64
    RunsComplete int
}

type Oracle struct {
    mu         sync.Mutex
    lifetime   float64
    perRunCap  float64
    spent      float64
    runSpent   float64
    path       string  // budget.json path
}

func New(lifetimeCap, perRunCap float64, persistPath string) *Oracle
  // Initialize oracle, load existing budget.json if present

func (o *Oracle) CanSpend(estimatedOutputTokens int) bool
  // Thread-safe. Returns false if estimated cost would exceed lifetime or per-run cap.
  // cost = estimatedOutputTokens * 0.28 / 1_000_000

func (o *Oracle) Record(inputTokens, outputTokens, cachedInputTokens int)
  // Thread-safe. Records actual spend from DeepSeek usage response.
  // freshInput = inputTokens - cachedInputTokens
  // inputCost = freshInput * 0.14/1e6 + cachedInputTokens * 0.0028/1e6
  // outputCost = outputTokens * 0.28/1e6
  // Updates spent, runSpent, persists to disk

func (o *Oracle) ResetRun()
  // Reset runSpent to 0 (called at start of new run)

func (o *Oracle) Status() Status
  // Thread-safe snapshot of current budget state

func (o *Oracle) persist() error
  // Write budget.json atomically (write tmp, rename)

depends_on: []
notes: "~50 LOC. No reservation/commit dance. Simple counter."
```

---

### `internal/config/config.go`

```
role: configuration
imports: [os, path/filepath, github.com/pelletier/go-toml/v2]
exports: [Config, Load, DefaultPath]
```

```go
// public_api:

type Config struct {
    Budget   BudgetConfig
    Output   OutputConfig
    Profile  string   // default stack profile name
    Daemon   DaemonConfig
}

type BudgetConfig struct {
    LifetimeCap float64  // default 2.00
    PerRunCap   float64  // default 0.25
}

type OutputConfig struct {
    Directory string  // default "./output"
    GitInit   bool    // default true
}

type DaemonConfig struct {
    PythonExecutable string  // default "python3"
}

func Load(path string) (*Config, error)
  // Read TOML from path, fill defaults for missing keys

func DefaultPath() string
  // Returns ~/.pragma/config.toml

func (c *Config) Save(path string) error
  // Write config back to TOML

depends_on: []
```

---

### `internal/config/profiles.go`

```
role: embedded_stack_profiles
imports: [embed, github.com/pelletier/go-toml/v2]
exports: [Profile, LoadProfile, ListProfiles]
```

```go
// public_api:

//go:embed profiles/*.toml
var profilesFS embed.FS

type Profile struct {
    Meta        ProfileMeta
    Framework   FrameworkConfig
    Database    DatabaseConfig
    Testing     TestingConfig
    Deployment  DeploymentConfig
    Patterns    string  // raw context block
    Engineering string  // raw context block
    Security    string  // raw context block
    Linter      LinterConfig
    Conformance map[string]bool  // conformance rule flags
}

type ProfileMeta struct {
    Name        string
    Language    string
    Version     string
    Description string
}

func LoadProfile(name string) (*Profile, error)
  // Load from embedded FS, parse TOML

func ListProfiles() []ProfileMeta
  // Return metadata for all embedded profiles

depends_on: []
notes: "Profiles are embedded at compile time. No runtime file loading."
```

---

### `internal/keyvault/keyring.go`

```
role: api_key_storage
imports: [github.com/zalando/go-keyring]
exports: [Keyring, DefaultService]
```

```go
// public_api:

const DefaultService = "pragma"

type Keyring struct {
    service string
}

func NewKeyring(service string) *Keyring

func (k *Keyring) Available() bool
  // Returns true if OS keyring is accessible

func (k *Keyring) Get(name string) (string, error)
  // Get key by name ("deepseek")

func (k *Keyring) Set(name, value string) error
  // Store key

func (k *Keyring) Delete(name string) error
  // Remove key

depends_on: []
notes: "Thin wrapper over zalando/go-keyring. ~40 LOC."
```

---

### `internal/daemon/lifecycle.go`

```
role: daemon_process_management
imports: [os/exec, context, time, fmt, path/filepath, os]
exports: [Lifecycle, New]
```

```go
// public_api:

type Lifecycle struct {
    cmd        *exec.Cmd
    sockPath   string
    pythonExe  string
    ctx        context.Context
    cancel     context.CancelFunc
}

func New(pythonExe string) *Lifecycle

func (l *Lifecycle) Start(ctx context.Context) error
  // Spawn: python3 -m pragma_daemon --socket <sockPath>
  // Wait for socket file to appear (max 10s)
  // Returns error if daemon fails to start

func (l *Lifecycle) Stop() error
  // Send SIGTERM, wait 5s, SIGKILL if still alive
  // Remove socket file

func (l *Lifecycle) Restart(ctx context.Context) error
  // Stop() then Start()

func (l *Lifecycle) SocketPath() string
  // Returns the UDS path

depends_on: []
```

---

### `internal/daemon/client.go`

```
role: json_rpc_client
imports: [net, encoding/json, context, sync, fmt, bufio]
exports: [Client, Response]
```

```go
// public_api:

type Response struct {
    Result json.RawMessage
    Error  *RPCError
}

type RPCError struct {
    Code    int
    Message string
    Data    json.RawMessage
}

type Client struct {
    conn    net.Conn
    mu      sync.Mutex  // protects writes
    nextID  int64
    pending map[int64]chan Response
    done    chan struct{}
}

func Connect(socketPath string) (*Client, error)
  // Dial Unix socket, start readLoop goroutine

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error)
  // Send JSON-RPC request, wait for matching response by ID
  // Respects ctx cancellation
  // Returns RPCError as Go error if response has error field

func (c *Client) Close() error
  // Close connection, stop readLoop

func (c *Client) Reconnect(socketPath string) error
  // Close existing, dial new socket, restart readLoop

depends_on: []
notes: "readLoop runs in background goroutine, dispatches responses to pending channels"
```

---

### `internal/daemon/health.go`

```
role: daemon_health_monitor
imports: [context, time, log]
exports: [HealthMonitor, Start]
```

```go
// public_api:

type HealthMonitor struct {
    client     *Client
    lifecycle  *Lifecycle
    interval   time.Duration  // 5 seconds
    maxFails   int            // 3
    fails      int
    restarts   int
    maxRestart int            // 3
}

func NewHealthMonitor(client *Client, lifecycle *Lifecycle) *HealthMonitor

func (h *HealthMonitor) Start(ctx context.Context)
  // Background goroutine:
  //   Every 5s: client.Call(ctx, "ping", nil)
  //   On failure: fails++
  //   If fails >= maxFails: lifecycle.Restart() + client.Reconnect()
  //   If restarts >= maxRestart: cancel context (fatal)

depends_on: [internal/daemon/client, internal/daemon/lifecycle]
```

---


### `internal/pipeline/state.go`

```
role: pipeline_state_types
imports: []
exports: [Phase, RunState]
```

```go
// public_api:

type Phase int
const (
    PhaseInterview Phase = iota
    PhaseResearching
    PhaseCompilingSpec
    PhaseSpecReview       // human gate
    PhasePlanningDAG
    PhaseDAGReview        // human gate
    PhaseGenerating
    PhaseConformance
    PhaseHealing
    PhaseComplete
    PhasePaused
    PhaseFailed
)

func (p Phase) String() string

type RunState struct {
    RunID          string
    Phase          Phase
    ProjectName    string
    Manifest       json.RawMessage  // requirements_manifest.json content
    Research       json.RawMessage  // research_context.json content
    Spec           json.RawMessage  // spec.json content
    DAG            json.RawMessage  // execution_dag.json content
    SliceIndex     int
    FilesCompleted []string
    FilesRemaining []string
    FilesFailed    []string
    CostSoFar      float64
    PausedAt       *time.Time
}

depends_on: []
```

---

### `internal/pipeline/events.go`

```
role: tui_event_bus
imports: []
exports: [Event, PhaseChangedEvent, FileCompletedEvent, BudgetUpdatedEvent, LogEvent, InterviewMessageEvent, SpecReadyEvent, DAGReadyEvent, CoverageReportEvent, ErrorEvent]
```

```go
// public_api:

type Event interface{ eventTag() }

type PhaseChangedEvent struct { From, To Phase }
type FileCompletedEvent struct { Path string; Duration time.Duration; Healed bool }
type BudgetUpdatedEvent struct { Status budget.Status }
type LogEvent struct { Level string; Message string }
type InterviewMessageEvent struct { Role string; Content string }
type SpecReadyEvent struct { Spec json.RawMessage; FileCount int; TestCount int }
type DAGReadyEvent struct { DAG json.RawMessage; SliceCount int; EstSeconds int }
type CoverageReportEvent struct { Passed int; Total int; Issues []string }
type ErrorEvent struct { Err error; Fatal bool }

// All implement eventTag() as empty method

depends_on: []
```

---

### `internal/pipeline/service.go`

```
role: pipeline_orchestrator
imports: [context, time, fmt, encoding/json, internal/budget, internal/daemon, internal/config]
exports: [Service, Config, New, Run]
```

```go
// public_api:

type Config struct {
    Oracle      *budget.Oracle
    Client      *daemon.Client
    Profile     *config.Profile
    RunID       string
    OutputDir   string
    EventCh     chan<- Event  // sends events to TUI
}

type Service struct {
    cfg   Config
    state RunState
}

func New(cfg Config) *Service

func (s *Service) Run(ctx context.Context) error
  // Main loop: walks through phases sequentially
  //   1. runInterview(ctx)      → produces manifest
  //   2. runResearch(ctx)       → produces research_context
  //   3. runSpecCompile(ctx)    → produces spec.json
  //   4. WAIT for SpecReview gate (user approves via event)
  //   5. runDAGPlan()           → produces execution_dag.json (deterministic)
  //   6. WAIT for DAGReview gate
  //   7. runCodegen(ctx)        → generates all files, parallel per slice
  //   8. runCoverageGate(ctx)   → checks completeness
  //   9. Emit PhaseComplete
  // On ctx.Done(): save checkpoint, return

func (s *Service) Resume(ctx context.Context, state RunState) error
  // Resume from saved checkpoint state

func (s *Service) ApproveSpec()
  // Unblocks the SpecReview gate

func (s *Service) ApproveDAG()
  // Unblocks the DAGReview gate

func (s *Service) State() RunState
  // Returns current state snapshot (for checkpoint saving)

// private methods:
func (s *Service) runInterview(ctx context.Context) error
func (s *Service) runResearch(ctx context.Context) error
func (s *Service) runSpecCompile(ctx context.Context) error
func (s *Service) runDAGPlan() error
func (s *Service) runCodegen(ctx context.Context) error
func (s *Service) runCoverageGate(ctx context.Context) error
func (s *Service) emit(e Event)
func (s *Service) saveCheckpoint() error

depends_on: [internal/budget, internal/daemon/client, internal/config, internal/pipeline/state, internal/pipeline/events]
notes: "This is the heart of Pragma. ~400 LOC. Each run* method is a JSON-RPC call to the daemon."
```

---

### `internal/pipeline/checkpoint.go`

```
role: checkpoint_persistence
imports: [encoding/json, os, path/filepath, time]
exports: [SaveCheckpoint, LoadCheckpoint, ListRuns]
```

```go
// public_api:

func SaveCheckpoint(state RunState, dir string) error
  // Write state to .pragma/runs/<run_id>/checkpoint.json

func LoadCheckpoint(runID string, dir string) (*RunState, error)
  // Read checkpoint.json for given run

func ListRuns(dir string) ([]RunSummary, error)
  // List all runs with their phase, project name, and paused_at

type RunSummary struct {
    RunID       string
    ProjectName string
    Phase       Phase
    PausedAt    *time.Time
}

depends_on: [internal/pipeline/state]
```

---


### `internal/tui/app.go`

```
role: tui_main_model
imports: [github.com/charmbracelet/bubbletea, internal/pipeline, internal/config, internal/budget]
exports: [Run]
```

```go
// public_api:

func Run(cfg *config.Config, oracle *budget.Oracle) error
  // Initialize Bubble Tea program with the root model
  // Root model manages screen switching based on pipeline events
  // Returns when user quits

// internal:
type model struct {
    screen      screen  // enum: home, onboarding, interview, researching, specReview, dagApproval, generating, complete, settings, resume
    cfg         *config.Config
    oracle      *budget.Oracle
    pipeline    *pipeline.Service
    eventCh     <-chan pipeline.Event
    width       int
    height      int
    // sub-models for each screen
    home        homeModel
    onboarding  onboardingModel
    interview   interviewModel
    specReview  specReviewModel
    dagApproval dagApprovalModel
    generating  generatingModel
    complete    completeModel
    settings    settingsModel
    resume      resumeModel
}

func (m model) Init() tea.Cmd
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (m model) View() string

depends_on: [internal/pipeline, internal/config, internal/budget, internal/tui/* (all screen files)]
notes: "~200 LOC. Delegates rendering to screen sub-models."
```

---

### `internal/tui/home.go`

```
role: home_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/lipgloss, internal/budget]
exports: [homeModel]
```

```go
// public_api:
type homeModel struct {
    selected int  // menu cursor position
    budget   budget.Status
    paused   int  // number of paused runs
}

func (m homeModel) Update(msg tea.Msg) (homeModel, tea.Cmd)
func (m homeModel) View() string
  // Renders: logo, menu (new/resume/settings/doctor/quit), budget bar

depends_on: [internal/budget]
```

---

### `internal/tui/onboarding.go`

```
role: first_run_setup_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/textinput]
exports: [onboardingModel]
```

```go
// public_api:
type onboardingModel struct {
    step      int  // 0=deepseek key, 1=budget, 2=profile
    keyInput  textinput.Model
    budget    float64
    profile   string
    err       error
}

func (m onboardingModel) Update(msg tea.Msg) (onboardingModel, tea.Cmd)
func (m onboardingModel) View() string
  // Renders: step-by-step key paste, budget set, profile selection

depends_on: [internal/keyvault, internal/config]
```

---

### `internal/tui/interview.go`

```
role: interview_chat_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/textinput, github.com/charmbracelet/bubbles/viewport]
exports: [interviewModel]
```

```go
// public_api:
type interviewModel struct {
    messages  []chatMessage  // {role, content}
    input     textinput.Model
    viewport  viewport.Model
    phase     string  // "Interview (2/5)"
    waiting   bool    // waiting for LLM response
}

type chatMessage struct {
    Role    string  // "user" or "assistant"
    Content string
}

func (m interviewModel) Update(msg tea.Msg) (interviewModel, tea.Cmd)
func (m interviewModel) View() string
  // Renders: scrollable chat history + text input + status bar

depends_on: []
```

---

### `internal/tui/spec_review.go`

```
role: spec_review_screen (human gate 1)
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/viewport, encoding/json]
exports: [specReviewModel]
```

```go
// public_api:
type specReviewModel struct {
    spec       specSummary
    tree       []treeNode  // file tree with summaries
    cursor     int
    expanded   bool  // detail view for selected file
    viewport   viewport.Model
}

type specSummary struct {
    FileCount     int
    EndpointCount int
    ModelCount    int
    TestCount     int
}

func (m specReviewModel) Update(msg tea.Msg) (specReviewModel, tea.Cmd)
func (m specReviewModel) View() string
  // Renders: summary stats, scrollable file tree, detail expand on 'd'
  // Keys: a=approve, e=edit($EDITOR), r=regenerate, d=detail, q=quit

depends_on: []
```

---

### `internal/tui/dag_approval.go`

```
role: dag_approval_screen (human gate 2)
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/viewport]
exports: [dagApprovalModel]
```

```go
// public_api:
type dagApprovalModel struct {
    slices     []dagSlice
    totalFiles int
    estTime    int  // seconds
    estCost    float64
    viewport   viewport.Model
}

type dagSlice struct {
    Index   int
    Files   []string
    Parallel bool
}

func (m dagApprovalModel) Update(msg tea.Msg) (dagApprovalModel, tea.Cmd)
func (m dagApprovalModel) View() string
  // Renders: slice list with file names, est time/cost
  // Keys: a=approve, e=edit, q=quit

depends_on: []
```

---

### `internal/tui/generating.go`

```
role: codegen_progress_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/progress, github.com/charmbracelet/bubbles/viewport]
exports: [generatingModel]
```

```go
// public_api:
type generatingModel struct {
    total       int
    completed   int
    healed      int
    failed      int
    files       []fileStatus  // {path, status, duration}
    progress    progress.Model
    viewport    viewport.Model
    speed       string  // "~4 files/sec"
    eta         int     // seconds remaining
}

type fileStatus struct {
    Path     string
    Status   string  // "done", "generating", "queued", "healed", "failed"
    Duration time.Duration
}

func (m generatingModel) Update(msg tea.Msg) (generatingModel, tea.Cmd)
func (m generatingModel) View() string
  // Renders: progress bar, live file list (scrollable), conformance/heal counts, speed/ETA

depends_on: []
```

---

### `internal/tui/complete.go`

```
role: completion_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/lipgloss, internal/budget]
exports: [completeModel]
```

```go
// public_api:
type completeModel struct {
    projectName string
    outputPath  string
    fileCount   int
    testCount   int
    coverage    int  // percent
    costBreak   []costLine  // {phase, cost}
    totalCost   float64
    budgetLeft  float64
}

type costLine struct {
    Phase string
    Cost  float64
}

func (m completeModel) Update(msg tea.Msg) (completeModel, tea.Cmd)
func (m completeModel) View() string
  // Renders: checkmark, output path, file/test count, cost breakdown, next steps, budget remaining

depends_on: [internal/budget]
```

---

### `internal/tui/settings.go`

```
role: settings_screen
imports: [github.com/charmbracelet/bubbletea, github.com/charmbracelet/bubbles/textinput, internal/config, internal/keyvault]
exports: [settingsModel]
```

```go
// public_api:
type settingsModel struct {
    section  int  // 0=keys, 1=budget, 2=profile, 3=output
    cfg      *config.Config
    keyring  *keyvault.Keyring
    editing  bool
    input    textinput.Model
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd)
func (m settingsModel) View() string
  // Renders: key display (masked), budget values, profile radio buttons, output dir

depends_on: [internal/config, internal/keyvault]
```

---

### `internal/tui/resume.go`

```
role: resume_screen
imports: [github.com/charmbracelet/bubbletea, internal/pipeline]
exports: [resumeModel]
```

```go
// public_api:
type resumeModel struct {
    runs     []pipeline.RunSummary
    cursor   int
}

func (m resumeModel) Update(msg tea.Msg) (resumeModel, tea.Cmd)
func (m resumeModel) View() string
  // Renders: list of paused runs with project name, phase, paused time
  // Keys: enter=resume, d=delete, esc=back

depends_on: [internal/pipeline]
```

---

### `internal/tui/styles.go`

```
role: shared_tui_styling
imports: [github.com/charmbracelet/lipgloss]
exports: [Styles]
```

```go
// public_api:
var (
    StyleTitle      lipgloss.Style
    StyleSubtitle   lipgloss.Style
    StyleSuccess    lipgloss.Style
    StyleWarning    lipgloss.Style
    StyleError      lipgloss.Style
    StyleMuted      lipgloss.Style
    StyleBorder     lipgloss.Style
    StyleStatusBar  lipgloss.Style
    StyleSelected   lipgloss.Style
)

depends_on: []
notes: "~30 LOC. Define once, use everywhere."
```

---


## Files — Python Side

---

### `daemon/pragma_daemon/main.py`

```
role: daemon_entrypoint
imports: [asyncio, argparse, sys, pragma_daemon.rpc]
exports: [main]
```

```python
# public_api:

async def main():
    # 1. Parse --socket <path> argument
    # 2. Create RPC server
    # 3. Register all method handlers
    # 4. Start listening on Unix socket
    # 5. Run until SIGTERM or connection close

if __name__ == "__main__":
    asyncio.run(main())

depends_on: [pragma_daemon.rpc]
```

---

### `daemon/pragma_daemon/rpc.py`

```
role: json_rpc_server
imports: [asyncio, json, typing]
exports: [RPCServer, handler]
```

```python
# public_api:

class RPCServer:
    def __init__(self, socket_path: str)

    def register(self, method: str, handler: Callable) -> None
        # Register a method name → async handler

    async def serve(self) -> None
        # Start Unix socket server, accept one connection (Go client)
        # Read newline-delimited JSON-RPC requests
        # Dispatch to registered handlers
        # Send responses with matching id

    async def notify(self, method: str, params: dict) -> None
        # Send a notification (no id, no response expected)

# internal:
    async def _handle_connection(self, reader, writer)
    async def _dispatch(self, writer, request: dict)
    async def _send(self, writer, response: dict)
        # HOLDS asyncio.Lock around writer.write + drain

    _writer_lock: asyncio.Lock  # prevents interleaved frames

depends_on: []
notes: "~120 LOC. The writer lock is CRITICAL — without it, concurrent responses corrupt the stream."
```

---

### `daemon/pragma_daemon/deepseek.py`

```
role: deepseek_api_client
imports: [httpx, json, typing, asyncio]
exports: [DeepSeekClient, ChatResponse, Usage]
```

```python
# public_api:

@dataclass
class Usage:
    input_tokens: int
    output_tokens: int
    cached_input_tokens: int

@dataclass
class ChatResponse:
    content: str
    reasoning_content: str | None  # only when thinking=True
    usage: Usage
    model: str

class DeepSeekClient:
    def __init__(self, api_key: str, base_url: str = "https://api.deepseek.com")

    async def chat(
        self,
        messages: list[dict],
        thinking: bool = False,
        reasoning_effort: str = "high",  # "high" | "low"
        max_tokens: int = 16384,
        temperature: float = 0.6,
    ) -> ChatResponse
        # POST /chat/completions
        # If thinking=True: adds {"thinking": {"type": "enabled"}, "reasoning_effort": effort}
        # Parses response, extracts usage, content, reasoning_content
        # Raises on HTTP errors (with retry-after for 429)

    async def chat_stream(
        self,
        messages: list[dict],
        thinking: bool = False,
        reasoning_effort: str = "high",
        max_tokens: int = 16384,
    ) -> AsyncIterator[str]
        # Same as chat() but streams content tokens
        # Yields content chunks as they arrive
        # Returns total Usage after stream ends

depends_on: []
notes: "~130 LOC. Single client, single endpoint. httpx with timeout=120s."
```

---

### `daemon/pragma_daemon/research.py`

```
role: research_resolver
imports: [httpx, asyncio, duckduckgo_search, typing]
exports: [resolve_research]
```

```python
# public_api:

ENTITY_REPOS: dict[str, str]  # ~100 entries: "stripe" → "stripe/stripe-python"

async def resolve_research(
    manifest: dict,
    language: str,
    max_queries: int = 5,
    timeout: float = 10.0,
) -> dict
    # 1. Extract domain entities from manifest description + constraints
    # 2. For each entity (max 5):
    #    a) Map to GitHub repo via ENTITY_REPOS
    #    b) Call deepwiki_ask(repo, question)
    #    c) If fails: ddg_search(query)
    # 3. Combine results into research_context (max 4096 tokens)
    # Returns: {"queries": [...], "findings": [...], "total_tokens": int}

async def deepwiki_ask(repo: str, question: str) -> str | None
    # POST https://mcp.deepwiki.com/mcp
    # JSON-RPC: method="tools/call", name="ask_question"
    # Returns answer text or None on failure

def ddg_search(query: str, max_results: int = 5) -> list[str]
    # Sync call to DDGS().text()
    # Returns list of snippet strings
    # Returns [] on any exception (graceful degradation)

depends_on: []
notes: "~80 LOC. All calls have 10s timeout. Failures are non-fatal."
```

---

### `daemon/pragma_daemon/spec_compiler.py`

```
role: three_pass_spec_compiler
imports: [json, typing, pragma_daemon.deepseek]
exports: [compile_spec]
```

```python
# public_api:

async def compile_spec(
    client: DeepSeekClient,
    manifest: dict,
    research_context: dict,
    profile: dict,  # parsed profile TOML as dict
) -> dict
    # 3-pass compilation:
    #   Pass 1 (thinking ON, high): draft full spec.json
    #   Pass 2 (thinking ON, high): optimize (fix cycles, gaps, security)
    #   Pass 3 (thinking OFF): finalize JSON (cleanup, validate structure)
    # Returns: parsed spec.json dict
    # Raises: SpecCompilationError on all 3 passes failing validation

async def _pass(
    client: DeepSeekClient,
    messages: list[dict],
    thinking: bool,
    effort: str,
) -> str
    # Single pass call. Returns raw content string.

def _build_system_prompt(profile: dict) -> str
    # Assembles: spec compiler instructions + profile.patterns + profile.engineering + profile.security

def _build_pass1_messages(system: str, manifest: dict, research: dict) -> list[dict]
def _build_pass2_messages(system: str, manifest: dict, research: dict, pass1_output: str) -> list[dict]
def _build_pass3_messages(system: str, pass2_output: str) -> list[dict]
    # Each builds the message array with cache-friendly prefix ordering

def _parse_spec_json(raw: str) -> dict
    # Extract JSON from response (strip markdown fences if present)
    # Validate required top-level keys
    # Raises ValueError on invalid JSON

depends_on: [pragma_daemon.deepseek]
notes: "~300 LOC. The crown jewel. Prompt structure follows §8.3 exactly."
```

---

### `daemon/pragma_daemon/spec_validator.py`

```
role: deterministic_spec_validation
imports: [typing]
exports: [validate_spec, ValidationError]
```

```python
# public_api:

@dataclass
class ValidationError:
    rule: str
    message: str
    file_path: str | None

def validate_spec(spec: dict) -> list[ValidationError]
    # Deterministic validation (no LLM):
    #   1. No dependency cycles in depends_on graph
    #   2. Every import resolves to a spec'd file or listed dependency
    #   3. Every endpoint has a corresponding route file
    #   4. At least one test file per module directory
    #   5. All explicit user constraints preserved
    #   6. No duplicate file paths
    #   7. Every relationship target exists
    # Returns: list of errors (empty = valid)

def _detect_cycles(spec: dict) -> list[str]
    # Topological sort, return cycle paths if found

def _check_imports(spec: dict) -> list[ValidationError]
def _check_endpoints(spec: dict) -> list[ValidationError]
def _check_tests(spec: dict) -> list[ValidationError]
def _check_relationships(spec: dict) -> list[ValidationError]

depends_on: []
notes: "~150 LOC. Pure logic, no I/O."
```

---

### `daemon/pragma_daemon/code_generator.py`

```
role: per_file_code_generation
imports: [typing, pragma_daemon.deepseek]
exports: [generate_file, generate_readme]
```

```python
# public_api:

async def generate_file(
    client: DeepSeekClient,
    file_contract: dict,       # single entry from spec.files[]
    profile: dict,             # parsed profile
    dependency_contents: dict, # {path: content} of already-generated depends_on files
    spec_summary: str,         # condensed spec context (~3K tokens)
) -> str
    # Single DeepSeek call (thinking OFF)
    # Returns: raw file content string (no fences, no explanation)

async def generate_readme(
    client: DeepSeekClient,
    spec: dict,
) -> str
    # DeepSeek call (thinking OFF) with layman-friendly README prompt
    # Returns: README.md content

def _build_codegen_system_prompt(profile: dict) -> str
    # "You are a code generator. Produce ONLY the file content..."
    # Includes profile.patterns

def _build_file_messages(
    system: str,
    file_contract: dict,
    dependency_contents: dict,
    spec_summary: str,
) -> list[dict]
    # Message array with stable prefix for cache hits

depends_on: [pragma_daemon.deepseek]
notes: "~150 LOC. One function per file, no batching logic (that's Go's job)."
```

---

### `daemon/pragma_daemon/conformance.py`

```
role: ast_conformance_checker
imports: [tree_sitter, pathlib, subprocess, json, typing]
exports: [check_file, check_coverage, ConformanceResult, CoverageReport]
```

```python
# public_api:

@dataclass
class ConformanceResult:
    passed: bool
    errors: list[str]  # human-readable error descriptions

@dataclass
class CoverageReport:
    passed: int
    total: int
    coverage_pct: float
    issues: list[tuple[str, ...]]  # (type, path, detail)

def check_file(
    file_path: Path,
    file_contract: dict,    # from spec.files[] entry
    profile_rules: dict,    # from profile.conformance_rules
    language: str,          # "python" | "typescript" | "go"
) -> ConformanceResult
    # 1. Parse with tree-sitter (syntax check)
    # 2. Extract exported names from AST
    # 3. Check all spec'd exports present
    # 4. Check function signatures match (name, arg count, return annotation)
    # 5. Run linter (ruff/eslint/go-vet) via subprocess
    # 6. Apply profile-specific rules (async handlers, use client, etc.)
    # Returns: ConformanceResult

def check_coverage(
    spec: dict,
    output_dir: Path,
    language: str,
) -> CoverageReport
    # Project-level completeness (§11.5):
    # 1. Every spec'd file exists on disk
    # 2. Every export present in AST
    # 3. Every test case has a function
    # 4. Every dependency in manifest
    # Returns: CoverageReport

# internal:
def _parse_python(path: Path) -> ASTInfo
def _parse_typescript(path: Path) -> ASTInfo
def _parse_go(path: Path) -> ASTInfo

def _run_linter(path: Path, language: str, profile_linter: dict) -> list[str]
    # subprocess.run(["ruff", "check", str(path)]) or equivalent
    # Returns list of lint error strings

def _check_profile_rules(path: Path, content: str, rules: dict, language: str) -> list[str]
    # Apply profile-specific checks (e.g., 'use client' for hooks)

@dataclass
class ASTInfo:
    exported_names: set[str]
    function_signatures: dict[str, FuncSig]  # name → {args, returns}
    has_directive: dict[str, bool]  # "use client" → True/False

depends_on: []
notes: "~300 LOC. Most complex Python file. tree-sitter does the heavy lifting."
```

---

### `daemon/pragma_daemon/cache.py`

```
role: l1_exact_hash_cache
imports: [hashlib, json, pathlib, typing, collections]
exports: [L1Cache]
```

```python
# public_api:

class L1Cache:
    def __init__(self, max_entries: int = 500, persist_path: Path | None = None)

    def get(self, messages: list[dict], thinking: bool) -> str | None
        # SHA-256 of (messages + thinking flag) → lookup in dict
        # Returns cached content or None

    def put(self, messages: list[dict], thinking: bool, content: str) -> None
        # Store in LRU dict. Evict oldest if over max_entries.

    def save(self) -> None
        # Persist to JSON file (if persist_path set)

    def load(self) -> None
        # Load from JSON file (if exists)

    def clear(self) -> None
        # Empty the cache

    @property
    def size(self) -> int

depends_on: []
notes: "~60 LOC. OrderedDict for LRU. Optional persistence."
```

---

### `daemon/pragma_daemon/methods.py`

```
role: rpc_method_registration
imports: [pragma_daemon.deepseek, pragma_daemon.research, pragma_daemon.spec_compiler, pragma_daemon.spec_validator, pragma_daemon.code_generator, pragma_daemon.conformance, pragma_daemon.cache]
exports: [register_methods]
```

```python
# public_api:

def register_methods(server: RPCServer, client: DeepSeekClient, cache: L1Cache) -> None
    # Registers all JSON-RPC methods:
    #
    # "ping" → returns "pong" (health check)
    # "interview" → params: {messages} → returns: {content, usage}
    # "research" → params: {manifest, language} → returns: {research_context}
    # "compile_spec" → params: {manifest, research, profile} → returns: {spec, usage}
    # "validate_spec" → params: {spec} → returns: {errors}
    # "generate_file" → params: {file_contract, profile, deps, summary} → returns: {content, usage}
    # "generate_readme" → params: {spec} → returns: {content, usage}
    # "check_file" → params: {file_path, contract, rules, language} → returns: {passed, errors}
    # "check_coverage" → params: {spec, output_dir, language} → returns: {report}
    # "heal_file" → params: {path, error, content, contract} → returns: {content, usage}

depends_on: [all daemon modules]
notes: "~200 LOC. Thin wrappers that call the actual implementation modules."
```

---


## Tests

---

### Go Tests

```
internal/budget/oracle_test.go:
  - TestCanSpend_UnderBudget
  - TestCanSpend_OverLifetime
  - TestCanSpend_OverRunCap
  - TestRecord_UpdatesSpent
  - TestRecord_CacheHitDiscount
  - TestPersist_WritesJSON
  - TestNew_LoadsExisting
  - TestResetRun_ZeroesRunSpent

internal/config/config_test.go:
  - TestLoad_DefaultValues
  - TestLoad_CustomValues
  - TestLoad_MissingFile_ReturnsDefaults
  - TestSave_RoundTrip

internal/config/profiles_test.go:
  - TestLoadProfile_FastAPI
  - TestLoadProfile_Express
  - TestLoadProfile_Unknown_ReturnsError
  - TestListProfiles_AllPresent

internal/daemon/client_test.go:
  - TestConnect_ValidSocket
  - TestCall_ReceivesResponse
  - TestCall_ContextCancelled
  - TestCall_RPCError
  - TestReconnect_AfterClose

internal/daemon/health_test.go:
  - TestHealthMonitor_PingSuccess
  - TestHealthMonitor_PingFail_Restarts
  - TestHealthMonitor_MaxRestarts_Fatal

internal/pipeline/service_test.go:
  - TestRun_FullPipeline_MockDaemon
  - TestRun_BudgetExhausted_Aborts
  - TestRun_CtrlC_SavesCheckpoint
  - TestResume_FromCheckpoint
  - TestApproveSpec_UnblocksGate
  - TestApproveDAG_UnblocksGate

internal/pipeline/checkpoint_test.go:
  - TestSaveCheckpoint_CreatesFile
  - TestLoadCheckpoint_RestoresState
  - TestListRuns_ReturnsAll
```

---

### Python Tests

```
daemon/tests/test_rpc.py:
  - test_server_handles_ping
  - test_server_handles_unknown_method
  - test_server_concurrent_requests_no_interleave
  - test_server_invalid_json_returns_parse_error

daemon/tests/test_deepseek.py:
  - test_chat_thinking_off
  - test_chat_thinking_on
  - test_chat_429_raises_with_retry_after
  - test_chat_timeout_raises
  - test_usage_parsing

daemon/tests/test_research.py:
  - test_resolve_with_known_entity
  - test_resolve_unknown_entity_falls_to_ddg
  - test_resolve_timeout_returns_partial
  - test_deepwiki_ask_success
  - test_deepwiki_ask_failure_returns_none
  - test_ddg_search_exception_returns_empty

daemon/tests/test_spec_compiler.py:
  - test_compile_3pass_produces_valid_json
  - test_compile_respects_constraints
  - test_compile_pass3_uses_thinking_off
  - test_build_system_prompt_includes_profile
  - test_parse_spec_json_strips_fences
  - test_parse_spec_json_invalid_raises

daemon/tests/test_spec_validator.py:
  - test_valid_spec_no_errors
  - test_cycle_detected
  - test_missing_import_target
  - test_missing_endpoint_handler
  - test_duplicate_file_path
  - test_missing_test_file
  - test_broken_relationship

daemon/tests/test_code_generator.py:
  - test_generate_file_returns_content
  - test_generate_file_no_fences
  - test_generate_readme_layman_friendly
  - test_codegen_prompt_includes_deps

daemon/tests/test_conformance.py:
  - test_check_file_valid_python
  - test_check_file_missing_export
  - test_check_file_signature_mismatch
  - test_check_file_lint_failure
  - test_check_file_profile_rule_async_handler
  - test_check_file_profile_rule_use_client
  - test_check_coverage_all_present
  - test_check_coverage_missing_file
  - test_check_coverage_missing_test

daemon/tests/test_cache.py:
  - test_put_get_hit
  - test_put_get_miss
  - test_lru_eviction
  - test_save_load_persistence
  - test_clear
```

---

## Deployment

```toml
[build]
go_binary = "go build -ldflags '-s -w' -o pragma ./cmd/pragma"
python_package = "cd daemon && pip install ."

[distribution]
# Option A: Single binary with embedded Python (PyInstaller or nuitka)
# Option B: Go binary + pip install pragma-daemon (two-step)
# Decision: Option B for v1 (simpler build, debug easier)

install_script = """
#!/bin/bash
# Install Go binary
go install github.com/sarv-projects/pragma/cmd/pragma@latest
# Install Python daemon
pip install pragma-daemon
"""

[ci]
# GitHub Actions: .github/workflows/ci.yml
# 1. go test ./...
# 2. cd daemon && pytest
# 3. golangci-lint run
# 4. cd daemon && ruff check .
```

---

## File Count Summary

| Component | Files | Est. LOC |
|---|---|---|
| `cmd/pragma/` | 2 | ~150 |
| `internal/budget/` | 1 (+1 test) | ~80 |
| `internal/config/` | 2 (+2 tests) | ~200 |
| `internal/keyvault/` | 1 | ~40 |
| `internal/daemon/` | 3 (+2 tests) | ~400 |
| `internal/pipeline/` | 3 (+2 tests) | ~600 |
| `internal/tui/` | 10 | ~1,800 |
| `daemon/pragma_daemon/` | 7 | ~1,100 |
| `daemon/tests/` | 8 | ~600 |
| `profiles/` | 6 TOML | ~500 |
| Config files | 5 (go.mod, pyproject, etc) | ~50 |
| **Total** | **~52 files** | **~5,520 LOC** |

---

## Build Order (DAG)

```
Slice 0 (parallel, no deps):
  internal/budget/oracle.go
  internal/config/config.go
  internal/config/profiles.go
  internal/keyvault/keyring.go
  internal/pipeline/state.go
  internal/pipeline/events.go
  internal/tui/styles.go
  daemon/pragma_daemon/rpc.py
  daemon/pragma_daemon/deepseek.py
  daemon/pragma_daemon/cache.py
  profiles/*.toml (all 6)

Slice 1 (depends on slice 0):
  internal/daemon/lifecycle.go
  internal/daemon/client.go
  daemon/pragma_daemon/research.py
  daemon/pragma_daemon/spec_validator.py

Slice 2 (depends on slice 0-1):
  internal/daemon/health.go
  internal/pipeline/checkpoint.go
  daemon/pragma_daemon/spec_compiler.py
  daemon/pragma_daemon/code_generator.py
  daemon/pragma_daemon/conformance.py

Slice 3 (depends on slice 0-2):
  daemon/pragma_daemon/methods.py
  daemon/pragma_daemon/main.py
  internal/pipeline/service.go

Slice 4 (depends on slice 0-3):
  internal/tui/home.go
  internal/tui/onboarding.go
  internal/tui/interview.go
  internal/tui/spec_review.go
  internal/tui/dag_approval.go
  internal/tui/generating.go
  internal/tui/complete.go
  internal/tui/settings.go
  internal/tui/resume.go
  internal/tui/app.go

Slice 5 (depends on all):
  cmd/pragma/main.go
  cmd/pragma/doctor.go

Slice 6 (tests, after all source):
  All *_test.go files
  All daemon/tests/test_*.py files
```

---

## End-to-End Smoke Test

After full build, this must pass:

```bash
# 1. Build
go build -o pragma ./cmd/pragma
cd daemon && pip install -e . && cd ..

# 2. Doctor
./pragma doctor  # exits 0 if DeepSeek key is set and API reachable

# 3. Headless run (CI-friendly)
echo '{"description": "A simple todo REST API with SQLite"}' | ./pragma --headless --budget 0.10
# Must produce: ./output/todo-api/ with:
#   - app/main.py (parseable Python)
#   - at least 5 files
#   - README.md
#   - Dockerfile
# Must exit 0
# Must report cost < $0.05

# 4. Unit tests
go test ./...          # all Go tests pass
cd daemon && pytest    # all Python tests pass
```

---

*End of Build Contract.*
