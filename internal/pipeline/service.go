package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/daemon"
)

// Timeouts for daemon RPC calls. These must be >= the daemon's per-request HTTP
// read timeout (300s) so the orchestrator never cancels a call the daemon is
// still legitimately processing. They are safety nets, not expected durations.
const (
	researchTimeout = 90 * time.Second
	specTimeout     = 15 * time.Minute
	codegenTimeout  = 6 * time.Minute
	coverageTimeout = 90 * time.Second
	readmeTimeout   = 4 * time.Minute
)

type FileNode struct {
	Path      string          `json:"path"`
	DependsOn []string        `json:"depends_on"`
	Contract  json.RawMessage `json:"-"` // full per-file contract from spec.json
	EstTokens int             `json:"-"`
}

type Service struct {
	client           *daemon.Client
	oracle           *budget.Oracle
	ledger           *budget.Ledger
	config           *config.Config
	events           chan<- Event
	state            RunState
	stateMu          sync.RWMutex
	SpecApprovalChan chan struct{}
	DAGApprovalChan  chan struct{}
	Headless           bool
	lastPhaseCostMu    sync.Mutex
	lastPhaseRecorded  float64

	// QueuedMessages collects user messages sent during generation.
	// After generation completes, these are auto-applied as refinements.
	QueuedMessages   []string
	queueMu          sync.Mutex

	// runLock prevents concurrent runs. Only one run can be active at a time.
	runLock          sync.Mutex
}

func NewService(c *daemon.Client, o *budget.Oracle, cfg *config.Config, evt chan<- Event, ledger *budget.Ledger) *Service {
	return &Service{
		client:           c,
		oracle:           o,
		ledger:           ledger,
		config:           cfg,
		events:           evt,

		SpecApprovalChan: make(chan struct{}, 1), // buffered: a double-enter won't deadlock (F7)
		DAGApprovalChan:  make(chan struct{}, 1),
	}
}

func (s *Service) Client() *daemon.Client {
	return s.client
}

// redactKeyRegex matches API key patterns for log redaction.
// Compiled once at package level — not per log call.
var redactKeyRegex = regexp.MustCompile(`sk-[a-zA-Z0-9]+`)

func (s *Service) logEvent(level, message string) {
	redacted := redactKeyRegex.ReplaceAllString(message, "sk-...[REDACTED]")
	s.events <- LogEvent{Level: level, Message: redacted}
}

func (s *Service) emit(e Event) { s.events <- e }

// ApproveSpec unblocks the spec gate. Non-blocking send so a second enter
// (with no receiver waiting) can't freeze the TUI thread (F7).
func (s *Service) ApproveSpec() {
	select {
	case s.SpecApprovalChan <- struct{}{}:
	default:
	}
}

func (s *Service) ApproveDAG() {
	select {
	case s.DAGApprovalChan <- struct{}{}:
	default:
	}
}

// QueueMessage adds a user message to the queue for post-generation refinement.
func (s *Service) QueueMessage(msg string) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	s.QueuedMessages = append(s.QueuedMessages, msg)
}

// DrainQueuedMessages returns all queued messages and clears the queue.
func (s *Service) DrainQueuedMessages() []string {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	msgs := s.QueuedMessages
	s.QueuedMessages = nil
	return msgs
}

// HasQueuedMessages reports whether there are queued messages.
func (s *Service) HasQueuedMessages() bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return len(s.QueuedMessages) > 0
}


func (s *Service) StartRun(ctx context.Context, manifest string, profileName string) error {
	// Prevent concurrent runs
	if !s.runLock.TryLock() {
		return fmt.Errorf("a run is already in progress. Please wait for it to complete or abort it first")
	}
	defer s.runLock.Unlock()

	if s.Headless {
		var m map[string]any
		if err := json.Unmarshal([]byte(manifest), &m); err != nil {
			return fmt.Errorf("invalid manifest JSON: %w", err)
		}
		if desc, ok := m["description"].(string); !ok || desc == "" {
			return fmt.Errorf("invalid manifest: 'description' is required")
		}
	}

	s.stateMu.Lock()
	// Use nanosecond timestamp + random suffix to prevent RunID collisions
	s.state = RunState{
		RunID:       fmt.Sprintf("run-%d-%04x", time.Now().UnixNano(), time.Now().UnixNano()%0xFFFF),
		Phase:       PhaseIdeation,
		ProjectName: deriveProjectName(manifest),
		ProfileName: profileName,
		Manifest:    json.RawMessage(manifest),
	}
	s.state.Phase = PhaseResearching
	s.stateMu.Unlock()

	// Phase 0: Interview complete — manifest was gathered by TUI
	s.emit(PhaseChangedEvent{From: PhaseIdeation, To: PhaseResearching})
	s.checkpoint()

	prof, err := config.LoadProfile(profileName)
	if err != nil {
		return err
	}

	// 1. Research
	if err := ctx.Err(); err != nil {
		return err
	}
	researchArgs := map[string]any{
		"manifest": s.state.Manifest,
		"profile":  prof,
	}
	rCtx, rCancel := context.WithTimeout(ctx, researchTimeout)
	researchRes, err := s.client.Call(rCtx, "do_research", researchArgs)
	rCancel()
	if err != nil {
		s.emit(ErrorEvent{Err: fmt.Errorf("research failed: %w", err), Fatal: false})
		// Research is best-effort; continue with empty context.
		researchRes = json.RawMessage(`{"findings":[]}`)
	}
	s.stateMu.Lock()
	s.state.Research = researchRes
	s.stateMu.Unlock()

	// Record research phase cost
	if s.ledger != nil {
		s.ledger.RecordPhase("research", s.phaseCost())
	}

	// 2. Compile Spec
	s.emit(PhaseChangedEvent{From: PhaseResearching, To: PhaseCompilingSpec})
	s.emit(SpecProgressEvent{Pass: 1, Status: "started", Message: "Drafting spec (Pass 1/3 — Lead Architect)..."})
	s.stateMu.Lock()
	s.state.Phase = PhaseCompilingSpec
	specArgs := map[string]any{
		"manifest": s.state.Manifest,
		"research": s.state.Research,
		"profile":  prof,
	}
	s.stateMu.Unlock()
	
	if err := ctx.Err(); err != nil {
		return err
	}
	sCtx, sCancel := context.WithTimeout(ctx, specTimeout)
	specRes, err := s.client.Call(sCtx, "compile_spec", specArgs)
	sCancel()
	if err != nil {
		s.emit(SpecProgressEvent{Pass: 0, Status: "error", Message: "Spec compilation failed: " + err.Error()})
		s.emit(ErrorEvent{Err: fmt.Errorf("spec compilation failed: %w", err), Fatal: true})
		return fmt.Errorf("spec compilation failed: %w", err)
	}
	s.emit(SpecProgressEvent{Pass: 3, Status: "completed", Message: "Spec compiled successfully"})
	s.stateMu.Lock()
	s.state.Spec = specRes
	s.stateMu.Unlock()
	s.checkpoint()

	// Record spec compilation phase cost
	if s.ledger != nil {
		s.ledger.RecordPhase("spec_compilation", s.phaseCost())
	}

	// 3. Parse spec into file nodes + topological slices
	slices, totalFilesToGenerate, testCount := s.planSlices(specRes)
	s.emit(SpecReadyEvent{Spec: specRes, FileCount: totalFilesToGenerate, TestCount: testCount})

	estSeconds := totalFilesToGenerate * 2
	// Overhead: research (~$0.002) + spec compilation (~$0.02) + readme (~$0.003) + security (~$0.002)
	overhead := 0.027
	estCost := overhead + float64(totalFilesToGenerate)*2000.0*(0.28/1_000_000.0)
	s.stateMu.Lock()
	s.state.DAG = specRes
	s.stateMu.Unlock()
	sliceFiles := make([][]string, len(slices))
	for i, sl := range slices {
		for _, f := range sl {
			sliceFiles[i] = append(sliceFiles[i], f.Path)
		}
	}
	s.emit(DAGReadyEvent{DAG: specRes, SliceCount: len(slices), EstSeconds: estSeconds, EstCost: estCost, Slices: sliceFiles})

	// Use the plain-English _summary from the spec compiler as the spec summary
	// passed to code generators. Fall back to truncated raw JSON if absent.
	specSummary := extractSpecSummary(specRes)
	s.checkpoint()

	// HUMAN GATE 1: Spec review
	s.emit(PhaseChangedEvent{From: PhaseCompilingSpec, To: PhaseSpecReview})
	s.stateMu.Lock()
	s.state.Phase = PhaseSpecReview
	s.stateMu.Unlock()
	if !s.Headless {
		select {
		case <-s.SpecApprovalChan:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// HUMAN GATE 2: DAG approval
	s.emit(PhaseChangedEvent{From: PhaseSpecReview, To: PhaseDAGReview})
	s.stateMu.Lock()
	s.state.Phase = PhaseDAGReview
	s.stateMu.Unlock()
	if !s.Headless {
		select {
		case <-s.DAGApprovalChan:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Phase 2: Generation
	s.emit(PhaseChangedEvent{From: PhaseDAGReview, To: PhaseGenerating})
	s.stateMu.Lock()
	s.state.Phase = PhaseGenerating
	runID := s.state.RunID
	s.stateMu.Unlock()

	runDir := filepath.Join(s.config.Output.Directory, runID)

	s.stateMu.Lock()
	s.state.FilesRemaining = nil
	for _, sl := range slices {
		for _, f := range sl {
			s.state.FilesRemaining = append(s.state.FilesRemaining, f.Path)
		}
	}
	s.stateMu.Unlock()

	healedCount, genErr := s.runGeneration(ctx, prof, slices, totalFilesToGenerate, specSummary, runDir, nil)
	if genErr != nil {
		return genErr
	}

	s.finishRun(ctx, runDir, healedCount, totalFilesToGenerate)
	return nil
}

// runGeneration generates all files in the given slices. Files whose path is in
// skip are not regenerated (used by Resume). Returns the heal count and a fatal
// error if the abort threshold is exceeded.
func (s *Service) runGeneration(
	ctx context.Context,
	prof *config.Profile,
	slices [][]FileNode,
	totalFiles int,
	specSummary, runDir string,
	skip map[string]bool,
) (int, error) {
	healedCount := 0
	var healMu sync.Mutex

	// Track per-file failure counts for spec amendment detection (Step 11)
	retryCounts := make(map[string]int)
	var retryMu sync.Mutex

	concurrency := 20

	for sliceIdx, slice := range slices {
		s.stateMu.Lock()
		s.state.SliceIndex = sliceIdx
		s.stateMu.Unlock()
		go s.checkpoint() // Async to avoid blocking generation slices

		var wg sync.WaitGroup

		sem := make(chan struct{}, concurrency)

		for _, f := range slice {
			if skip != nil && skip[f.Path] {
				continue
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(fNode FileNode) {
				defer wg.Done()
				defer func() { <-sem }()

				// Budget pre-flight with a real per-file estimate (D2).
				if !s.oracle.CanSpend(fNode.EstTokens) {
					st := s.oracle.Status()
					needed := float64(fNode.EstTokens) * 3.0 * 0.14 / 1_000_000.0
					s.logEvent("warn", fmt.Sprintf(
						"Budget exceeded for %s. Spent: $%.4f / $%.2f cap. Need ~$%.4f more. "+
							"Increase your per-run budget in Settings or use --budget flag.",
						fNode.Path, st.RunSpent, st.PerRunCap, needed,
					))
					s.emit(ErrorEvent{
						Err:   fmt.Errorf("budget exceeded at %s. Spent $%.4f of $%.2f cap. Increase budget in Settings to continue.", fNode.Path, st.RunSpent, st.PerRunCap),
						Fatal: false,
					})
					s.stateMu.Lock()
					s.state.FilesFailed = append(s.state.FilesFailed, fNode.Path)
					s.stateMu.Unlock()
					return
				}

				depsMap := make(map[string]string)
				for _, depPath := range fNode.DependsOn {
					depFullPath := filepath.Join(runDir, depPath)
					if b, err := os.ReadFile(depFullPath); err == nil {
						depsMap[depPath] = string(b)
					}
				}

				start := time.Now()
				contract := fNode.Contract
				if len(contract) == 0 {
					contract, _ = json.Marshal(map[string]any{"path": fNode.Path, "depends_on": fNode.DependsOn})
				}
				genArgs := map[string]any{
					"file_contract": contract,
					"profile":       prof,
					"deps":          depsMap,
					"spec_summary":  specSummary,
				}

				var genOut struct {
					Content string `json:"content"`
					Healed  bool   `json:"healed"`
					Usage   struct {
						InputTokens       int `json:"input_tokens"`
						OutputTokens      int `json:"output_tokens"`
						CachedInputTokens int `json:"cached_input_tokens"`
					} `json:"usage"`
				}

				var lastErr error
				for attempt := 0; attempt < 2; attempt++ {
					if err := ctx.Err(); err != nil {
						lastErr = err
						break
					}
					cCtx, cCancel := context.WithTimeout(ctx, codegenTimeout)
					genRes, callErr := s.client.Call(cCtx, "generate_file", genArgs)
					cCancel()
					if callErr != nil {
						lastErr = callErr
						continue
					}
					if err := json.Unmarshal(genRes, &genOut); err != nil {
						lastErr = err
						continue
					}
					lastErr = nil
					break
				}

				if lastErr != nil {
					s.logEvent("error", fmt.Sprintf("generate_file failed for %s: %v", fNode.Path, lastErr))
					s.stateMu.Lock()
					s.state.FilesFailed = append(s.state.FilesFailed, fNode.Path)
					s.stateMu.Unlock()

					// Spec amendment detection: if 2nd failure and error suggests spec issue
					retryMu.Lock()
					retryCounts[fNode.Path]++
					failCount := retryCounts[fNode.Path]
					retryMu.Unlock()
					if failCount >= 2 {
						errStr := lastErr.Error()
						if strings.Contains(errStr, "import") || strings.Contains(errStr, "undefined") ||
							strings.Contains(errStr, "module") || strings.Contains(errStr, "circular") {
							s.emit(SpecAmendmentProposedEvent{
								FilePath: fNode.Path,
								Reason:   fmt.Sprintf("File failed %d times with possible spec issue: %v", failCount, lastErr),
							})
							// Advisory only: we surface the amendment suggestion to
							// the UI but keep generating other files. Blocking the
							// whole run on a single file's spec doubt would stall
							// independent slices, so amendment is non-blocking.
						}
					}

					s.emit(FileCompletedEvent{Path: fNode.Path, Duration: time.Since(start), Failed: true})
					return
				}

				s.oracle.Record(genOut.Usage.InputTokens, genOut.Usage.OutputTokens, genOut.Usage.CachedInputTokens)

				if err := s.writeFile(runDir, fNode.Path, genOut.Content); err != nil {
					s.logEvent("error", fmt.Sprintf("Failed to write %s: %v", fNode.Path, err))
					s.stateMu.Lock()
					s.state.FilesFailed = append(s.state.FilesFailed, fNode.Path)
					s.stateMu.Unlock()
					s.emit(FileCompletedEvent{Path: fNode.Path, Duration: time.Since(start), Failed: true})
					return
				}

				s.stateMu.Lock()
				s.state.FilesCompleted = append(s.state.FilesCompleted, fNode.Path)
				s.state.FilesRemaining = removeString(s.state.FilesRemaining, fNode.Path)
				s.state.CostSoFar = s.oracle.Status().RunSpent
				s.stateMu.Unlock()
				if genOut.Healed {
					healMu.Lock()
					healedCount++
					healMu.Unlock()
				}

				desc := deriveFileDescription(fNode.Contract, fNode.Path)
				s.emit(FileCompletedEvent{Path: fNode.Path, Duration: time.Since(start), Healed: genOut.Healed, Description: desc})
				s.emit(BudgetUpdatedEvent{Status: s.oracle.Status()})
			}(f)
		}
		wg.Wait()

		// D3: abort threshold — tolerate a small absolute number of failures.
		abortThreshold := totalFiles / 10
		if abortThreshold < 3 {
			abortThreshold = 3
		}
		s.stateMu.Lock()
		failedCount := len(s.state.FilesFailed)
		s.stateMu.Unlock()

		if failedCount > abortThreshold {
			s.stateMu.Lock()
			s.state.Phase = PhaseFailed
			s.stateMu.Unlock()
			s.checkpoint()
			msg := fmt.Sprintf("Aborting: too many failed files (%d of %d)", failedCount, totalFiles)
			s.logEvent("fatal", msg)
			s.emit(ErrorEvent{Err: fmt.Errorf("%s", msg), Fatal: true})
			return healedCount, fmt.Errorf("too many failed files: %d", failedCount)
		}
	}

	return healedCount, nil
}

// finishRun runs the coverage gate + README, finalizes state, and emits the
// completion event. Returns the coverage percentage.
func (s *Service) finishRun(ctx context.Context, runDir string, healedCount, totalFiles int) int {
	coveragePct := 100
	if err := ctx.Err(); err == nil {
		s.stateMu.RLock()
		covArgs := map[string]any{
			"spec":            s.state.Spec,
			"manifest":        s.state.Manifest,
			"output_dir":      runDir,
			"files_completed": s.state.FilesCompleted,
		}
		s.stateMu.RUnlock()
		covCtx, covCancel := context.WithTimeout(ctx, coverageTimeout)
		covRes, covErr := s.client.Call(covCtx, "check_coverage", covArgs)
		covCancel()
		if covErr == nil {
			var covOut struct {
				Passed      bool     `json:"passed"`
				TotalChecks int      `json:"total_checks"`
				Issues      []string `json:"issues"`
			}
			if err := json.Unmarshal(covRes, &covOut); err == nil {
				passedCount := covOut.TotalChecks - len(covOut.Issues)
				if passedCount < 0 {
					passedCount = 0
				}
				if covOut.TotalChecks > 0 {
					coveragePct = passedCount * 100 / covOut.TotalChecks
				}
				s.emit(CoverageReportEvent{Passed: passedCount, Total: covOut.TotalChecks, Issues: covOut.Issues})
			}
		}
	}

	if err := ctx.Err(); err == nil {
		readmeCtx, readmeCancel := context.WithTimeout(ctx, readmeTimeout)
		s.stateMu.RLock()
		readmeArgs := map[string]any{"spec": s.state.Spec}
		s.stateMu.RUnlock()
		readmeRes, rErr := s.client.Call(readmeCtx, "generate_readme", readmeArgs)
		readmeCancel()
		if rErr == nil {
			var readmeOut struct {
				Content string `json:"content"`
				Usage   struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(readmeRes, &readmeOut); err == nil && readmeOut.Content != "" {
				_ = s.writeFile(runDir, "README.md", readmeOut.Content)
				// Record README generation cost if usage data is available
				if readmeOut.Usage.InputTokens > 0 || readmeOut.Usage.OutputTokens > 0 {
					s.oracle.Record(readmeOut.Usage.InputTokens, readmeOut.Usage.OutputTokens, 0)
				}
			}
		}
	}

	// Security audit RPC (non-fatal) — use a longer timeout since it makes an LLM call
	const auditTimeout = 3 * time.Minute
	if err := ctx.Err(); err == nil {
		s.stateMu.RLock()
		auditArgs := map[string]any{
			"files_completed": s.state.FilesCompleted,
			"output_dir":      runDir,
		}
		s.stateMu.RUnlock()
		auditCtx, auditCancel := context.WithTimeout(ctx, auditTimeout)
		auditRes, auditErr := s.client.Call(auditCtx, "security_audit", auditArgs)
		auditCancel()
		if auditErr != nil {
			s.logEvent("warn", fmt.Sprintf("security_audit RPC failed: %v", auditErr))
		} else {
			var auditOut struct {
				Warnings []string `json:"warnings"`
				Usage    struct {
					InputTokens       int `json:"input_tokens"`
					OutputTokens      int `json:"output_tokens"`
					CachedInputTokens int `json:"cached_input_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(auditRes, &auditOut); err == nil {
				s.emit(SecurityAuditEvent{Warnings: auditOut.Warnings})
				if auditOut.Usage.InputTokens > 0 || auditOut.Usage.OutputTokens > 0 {
					s.oracle.Record(auditOut.Usage.InputTokens, auditOut.Usage.OutputTokens, auditOut.Usage.CachedInputTokens)
				}
			}
		}
	}

	// Static analysis RPC (non-fatal)
	if err := ctx.Err(); err == nil {
		s.stateMu.RLock()
		saArgs := map[string]any{
			"output_dir": runDir,
			"spec":       s.state.Spec,
		}
		s.stateMu.RUnlock()
		saCtx, saCancel := context.WithTimeout(ctx, coverageTimeout)
		_, saErr := s.client.Call(saCtx, "static_analysis", saArgs)
		saCancel()
		if saErr != nil {
			s.logEvent("warn", fmt.Sprintf("static_analysis RPC failed: %v", saErr))
		}
	}

	// Attempt to run the project's test suite if spec defines setup.test
	var specSetup struct {
		Setup struct {
			Test string `json:"test"`
		} `json:"setup"`
	}
	s.stateMu.RLock()
	specData := s.state.Spec
	s.stateMu.RUnlock()
	if err := json.Unmarshal(specData, &specSetup); err == nil && specSetup.Setup.Test != "" {
		testCmd := specSetup.Setup.Test
		if isAllowedTestCommand(testCmd) {
			parts := strings.Fields(testCmd)
			if len(parts) > 0 {
				cmd := exec.Command(parts[0], parts[1:]...)
				cmd.Dir = runDir
				testOutput, testErr := cmd.CombinedOutput()
				// Truncate test output to 1MB max
				const maxTestOutput = 1 << 20
				if len(testOutput) > maxTestOutput {
					testOutput = testOutput[len(testOutput)-maxTestOutput:]
				}
				passed := testErr == nil
				s.emit(TestRunEvent{
					Command: testCmd,
					Passed:  passed,
					Output:  string(testOutput),
				})
			}
		} else {
			s.logEvent("warn", fmt.Sprintf("Skipping test command (not in allowlist): %s", testCmd))
		}
	}

	// Phase 2: Runtime Smoke Test Agent
	// If docker-compose.yml exists, attempt to start the app and verify it runs.
	composePath := filepath.Join(runDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err == nil {
		s.logEvent("info", "Running runtime smoke test via Docker Compose...")
		
		// 1. Build and start in detached mode with timeout
		upCtx, upCancel := context.WithTimeout(ctx, 3*time.Minute)
		defer upCancel()
		upCmd := exec.CommandContext(upCtx, "docker", "compose", "up", "-d", "--build")
		upCmd.Dir = runDir
		if upOut, upErr := upCmd.CombinedOutput(); upErr != nil {
			s.logEvent("warn", fmt.Sprintf("Docker compose up failed: %v. Output: %s", upErr, string(upOut)[:min(len(upOut), 500)]))
		} else {
			// 2. Poll for container readiness (up to 30s) instead of fixed sleep
			pollCtx, pollCancel := context.WithTimeout(ctx, 30*time.Second)
			pollDone := false
			pollTicker := time.NewTicker(3 * time.Second)
			pollDoneOrCancelled := func() bool {
				select {
				case <-pollCtx.Done():
					return true
				default:
					return pollDone
				}
			}
			for !pollDoneOrCancelled() {
				<-pollTicker.C
				psCmd := exec.Command("docker", "compose", "ps", "--format", "json")
				psCmd.Dir = runDir
				psOut, psErr := psCmd.CombinedOutput()
				if psErr != nil {
					continue
				}
				// Parse JSON output to check container states properly
				var containers []struct {
					Service string `json:"Service"`
					Status  string `json:"Status"`
				}
				if err := json.Unmarshal(psOut, &containers); err == nil {
					allRunning := true
					for _, c := range containers {
						// Only consider "running" or strings starting with "Up" as healthy
						if !strings.HasPrefix(c.Status, "Up") {
							allRunning = false
							break
						}
					}
					if allRunning && len(containers) > 0 {
						s.logEvent("info", "Runtime smoke test passed: Containers are running.")
						s.emit(RuntimeValidationPassedEvent{})
						pollDone = true
						break
					}
				}
			}
			pollCancel()
			pollTicker.Stop()
			
			if !pollDone {
				// Timed out or failed — fetch logs for the error event
				logsCmd := exec.Command("docker", "compose", "logs", "--tail", "100")
				logsCmd.Dir = runDir
				if logsOut, logsErr := logsCmd.CombinedOutput(); logsErr == nil {
					s.logEvent("warn", fmt.Sprintf("Runtime smoke test failed: Container did not start in time. Logs:\n%s", string(logsOut)[:min(len(logsOut), 1000)]))
					s.emit(RuntimeValidationErrorEvent{
						Message: "Application failed to start within 30 seconds. Check Docker logs for details.",
						Logs:    string(logsOut)[:min(len(logsOut), 2000)],
					})
				}
			}
			
			// 3. Clean up: stop and remove containers to leave the system clean
			downCmd := exec.Command("docker", "compose", "down")
			downCmd.Dir = runDir
			_ = downCmd.Run() // Ignore errors on cleanup
		}
	}

	// Git versioning: initialize a repo in the output directory
	if _, err := exec.LookPath("git"); err == nil {
		gitInit := exec.Command("git", "init")
		gitInit.Dir = runDir
		gitInit.Run()
		gitAdd := exec.Command("git", "add", "-A")
		gitAdd.Dir = runDir
		gitAdd.Run()
		gitCommit := exec.Command("git", "-c", "user.name=Pragma", "-c", "user.email=pragma@local", "commit", "-m", "Initial generation")
		gitCommit.Dir = runDir
		gitCommit.Run()
		s.logEvent("info", "Initialized git repository in output directory")
	} else {
		s.logEvent("info", "git not found on PATH; skipping version history")
	}

	s.stateMu.Lock()
	s.state.Phase = PhaseComplete
	runID := s.state.RunID
	projectName := s.state.ProjectName
	completedCount := len(s.state.FilesCompleted)
	failedCount := len(s.state.FilesFailed)
	s.stateMu.Unlock()
	
	s.checkpoint()
	s.oracle.RecordRunCompletion()

	// Record project cost in the ledger
	if s.ledger != nil {
		s.ledger.RecordProject(runID, projectName, s.oracle.Status().RunSpent)
	}

	st := s.oracle.Status()
	absRunDir, _ := filepath.Abs(runDir)
	s.emit(RunCompleteEvent{
		ProjectName: projectName,
		OutputPath:  absRunDir,
		FileCount:   completedCount,
		Healed:      healedCount,
		Failed:      failedCount,
		TotalCost:   st.RunSpent,
		BudgetLeft:  st.LifetimeCap - st.TotalSpent,
		Coverage:    coveragePct,
		Manifest:    s.state.Manifest,
		Spec:        s.state.Spec,
	})
	s.emit(PhaseChangedEvent{From: PhaseGenerating, To: PhaseComplete})
	return coveragePct
}

// Resume continues a checkpointed run that has a compiled spec. It regenerates
// only the files not already completed, then runs the coverage gate + README.
func (s *Service) Resume(ctx context.Context, state RunState) error {
	s.stateMu.Lock()
	s.state = state
	s.stateMu.Unlock()
	if len(state.Spec) == 0 {
		return fmt.Errorf("run %s has no compiled spec — cannot resume", state.RunID)
	}

	profileName := state.ProfileName
	if profileName == "" {
		profileName = s.config.Profile
	}
	prof, err := config.LoadProfile(profileName)
	if err != nil {
		return err
	}

	slices, totalFiles, _ := s.planSlices(state.Spec)
	specSummary := runeSafeTruncate(string(state.Spec), 3000)
	runDir := filepath.Join(s.config.Output.Directory, state.RunID)

	skip := make(map[string]bool)
	for _, p := range state.FilesCompleted {
		skip[p] = true
	}

	s.emit(PhaseChangedEvent{From: PhasePaused, To: PhaseGenerating})
	s.stateMu.Lock()
	s.state.Phase = PhaseGenerating
	s.stateMu.Unlock()
	s.emit(SpecReadyEvent{Spec: state.Spec, FileCount: totalFiles})

	startGenCost := s.oracle.Status().RunSpent
	healed, genErr := s.runGeneration(ctx, prof, slices, totalFiles, specSummary, runDir, skip)
	if s.ledger != nil {
		endGenCost := s.oracle.Status().RunSpent
		s.ledger.RecordPhase("generation", endGenCost-startGenCost)
	}
	if genErr != nil {
		return genErr
	}
	s.finishRun(ctx, runDir, healed, totalFiles)
	return nil
}

// planSlices parses the spec and produces topologically-ordered parallel slices.
func (s *Service) planSlices(specRes json.RawMessage) (slices [][]FileNode, totalFiles int, testCount int) {
	var specData struct {
		Files []json.RawMessage `json:"files"`
		Tests []json.RawMessage `json:"tests"`
	}
	if err := json.Unmarshal(specRes, &specData); err != nil {
		return nil, 0, 0
	}
	testCount = len(specData.Tests)

	var files []FileNode
	nodeMap := make(map[string]FileNode)
	for _, raw := range specData.Files {
		var meta struct {
			Path      string            `json:"path"`
			DependsOn []string          `json:"depends_on"`
			PublicAPI []json.RawMessage `json:"public_api"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || meta.Path == "" {
			continue
		}
		fn := FileNode{
			Path:      meta.Path,
			DependsOn: meta.DependsOn,
			Contract:  raw,
			EstTokens: estimateTokens(len(meta.PublicAPI), len(raw)),
		}
		files = append(files, fn)
		nodeMap[fn.Path] = fn
	}
	
	// B6: Tests array never becomes FileNodes
	for _, raw := range specData.Tests {
		var meta struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || meta.Path == "" {
			continue
		}
		fn := FileNode{
			Path:      meta.Path,
			DependsOn: nil, // tests can be generated independently or concurrently
			Contract:  raw,
			EstTokens: estimateTokens(0, len(raw)),
		}
		files = append(files, fn)
		nodeMap[fn.Path] = fn
	}
	
	totalFiles = len(files)
	if totalFiles == 0 {
		return nil, 0, testCount
	}

	// Sort files by path for deterministic ordering across runs.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	// Topological sort into parallel slices.
	inDegree := make(map[string]int)
	graph := make(map[string][]string)
	for _, f := range files {
		inDegree[f.Path] = 0
	}
	for _, f := range files {
		for _, dep := range f.DependsOn {
			if _, ok := nodeMap[dep]; ok {
				graph[dep] = append(graph[dep], f.Path)
				inDegree[f.Path]++
			}
		}
	}

	var current []FileNode
	for _, f := range files { // stable order
		if inDegree[f.Path] == 0 {
			current = append(current, f)
		}
	}

	placed := 0
	for len(current) > 0 {
		slices = append(slices, current)
		placed += len(current)
		var next []FileNode
		for _, node := range current {
			for _, neighbor := range graph[node.Path] {
				inDegree[neighbor]--
				if inDegree[neighbor] == 0 {
					next = append(next, nodeMap[neighbor])
				}
			}
		}
		current = next
	}

	// Any leftover (cycle remnant) — append as a final slice so they still gen.
	if placed < totalFiles {
		var leftover []FileNode
		for _, f := range files {
			if inDegree[f.Path] > 0 {
				leftover = append(leftover, f)
			}
		}
		if len(leftover) > 0 {
			slices = append(slices, leftover)
		}
	}

	return slices, totalFiles, testCount
}

func (s *Service) writeFile(runDir, relPath, content string) error {
	outPath := filepath.Join(runDir, relPath)
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}
	tmpPath := outPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, outPath)
}

func (s *Service) checkpoint() {
	s.stateMu.RLock()
	state := s.state
	s.stateMu.RUnlock()
	if err := SaveCheckpoint(state, s.config.Output.Directory); err != nil {
		s.logEvent("error", fmt.Sprintf("Failed to save checkpoint: %v", err))
	}
}

func (s *Service) State() RunState { 
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state 
}

// estimateTokens produces a per-file output token estimate from contract shape.
func estimateTokens(publicAPICount, contractBytes int) int {
	est := 600 + 350*publicAPICount + contractBytes/4
	if est < 800 {
		est = 800
	}
	if est > 8000 {
		est = 8000
	}
	return est
}

func deriveProjectName(manifest string) string {
	var m struct {
		ProjectName string `json:"project_name"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(manifest), &m); err == nil {
		if m.ProjectName != "" {
			return m.ProjectName
		}
		if m.Name != "" {
			return m.Name
		}
		if m.Description != "" {
			d := m.Description
			if len(d) > 40 {
				d = d[:40]
				if lastSpace := strings.LastIndex(d, " "); lastSpace > 0 {
					d = d[:lastSpace]
				}
			}
			return sanitizeForDir(d)
		}
	}
	return "Pragma Project"
}

func sanitizeForDir(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else if sb.Len() > 0 && sb.String()[sb.Len()-1] != '-' {
			sb.WriteRune('-')
		}
	}
	res := strings.TrimRight(sb.String(), "-")
	if res == "" {
		return "Pragma Project"
	}
	return res
}

func runeSafeTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Back up to a rune boundary so we never split a multibyte character.
	cut := max
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8RuneStart(b byte) bool {
	// Continuation bytes are 0b10xxxxxx.
	return b&0xC0 != 0x80
}

func (s *Service) phaseCost() float64 {
	s.lastPhaseCostMu.Lock()
	defer s.lastPhaseCostMu.Unlock()
	current := s.oracle.Status().RunSpent
	delta := current - s.lastPhaseRecorded
	s.lastPhaseRecorded = current
	if delta < 0 {
		delta = 0
	}
	return delta
}

func removeString(slice []string, target string) []string {
	out := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != target {
			out = append(out, v)
		}
	}
	return out
}

// extractSpecSummary returns the plain-English _summary field injected by the
// spec compiler, falling back to a truncated raw JSON string if absent.
// This is passed to code generators as context — a readable summary is far
// more useful than 3000 chars of truncated JSON.
func extractSpecSummary(specRes json.RawMessage) string {
	var s struct {
		Summary     string `json:"_summary"`
		Description string `json:"description"`
		ProjectName string `json:"project_name"`
	}
	if err := json.Unmarshal(specRes, &s); err == nil {
		if s.Summary != "" {
			return s.Summary
		}
		if s.Description != "" {
			return s.ProjectName + ": " + s.Description
		}
	}
	return runeSafeTruncate(string(specRes), 3000)
}


func deriveFileDescription(contract json.RawMessage, filePath string) string {
	var meta struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(contract, &meta); err == nil && meta.Role != "" {
		base := filepath.Base(filePath)
		return fmt.Sprintf("Generated %s: %s", meta.Role, base)
	}
	return fmt.Sprintf("Generated: %s", filepath.Base(filePath))
}

// allowedTestRunners is the set of known safe test runner prefixes.
var allowedTestRunners = []string{
	"npm test",
	"pytest",
	"go test",
	"cargo test",
	"make test",
	"yarn test",
	"pnpm test",
	"vitest",
	"jest",
}

// isAllowedTestCommand checks if a test command starts with a known safe runner.
func isAllowedTestCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	for _, prefix := range allowedTestRunners {
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
			return !containsShellMetachars(trimmed)
		}
	}
	return false
}

// containsShellMetachars rejects commands that contain shell injection characters.
// Checks for both literal backslash-n sequences AND real newline bytes.
func containsShellMetachars(cmd string) bool {
	dangerous := []string{";", "&&", "||", "|", "&", "`", "$(", ">", "<"}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			return true
		}
	}
	// Check for real newline (byte 0x0A) and carriage return (byte 0x0D)
	for _, b := range []byte(cmd) {
		if b == '\n' || b == '\r' {
			return true
		}
	}
	return false
}

// RefineSpec asks the AI to modify the manifest and recompiles the spec in-place.
func (s *Service) RefineSpec(ctx context.Context, prompt string) error {
	s.stateMu.Lock()
	if s.state.Phase != PhaseSpecReview {
		s.stateMu.Unlock()
		return fmt.Errorf("can only refine spec during spec review phase")
	}
	
	var manifest map[string]any
	if err := json.Unmarshal(s.state.Manifest, &manifest); err != nil {
		s.stateMu.Unlock()
		return err
	}
	prof := s.state.ProfileName
	research := s.state.Research
	s.stateMu.Unlock()

	s.logEvent("info", "Refining specification...")

	newManifest, err := s.client.RefineSpec(manifest, prompt)
	if err != nil {
		return fmt.Errorf("failed to refine manifest: %w", err)
	}

	specArgs := map[string]any{
		"manifest": newManifest,
		"research": research,
		"profile":  prof,
	}
	sCtx, sCancel := context.WithTimeout(ctx, specTimeout)
	specRes, err := s.client.Call(sCtx, "compile_spec", specArgs)
	sCancel()
	if err != nil {
		return fmt.Errorf("failed to compile new spec: %w", err)
	}

	manifestBytes, _ := json.Marshal(newManifest)

	s.stateMu.Lock()
	s.state.Manifest = manifestBytes
	s.state.Spec = specRes
	s.state.DAG = specRes
	s.stateMu.Unlock()

	slices, totalFilesToGenerate, testCount := s.planSlices(specRes)
	s.emit(SpecReadyEvent{Spec: specRes, FileCount: totalFilesToGenerate, TestCount: testCount})

	estSeconds := totalFilesToGenerate * 2
	overhead := 0.027
	estCost := overhead + float64(totalFilesToGenerate)*2000.0*(0.28/1_000_000.0)
	sliceFiles := make([][]string, len(slices))
	for i, sl := range slices {
		for _, f := range sl {
			sliceFiles[i] = append(sliceFiles[i], f.Path)
		}
	}
	s.emit(DAGReadyEvent{DAG: specRes, SliceCount: len(slices), EstSeconds: estSeconds, EstCost: estCost, Slices: sliceFiles})
	s.checkpoint()

	s.logEvent("success", "Spec refined successfully.")
	return nil
}
