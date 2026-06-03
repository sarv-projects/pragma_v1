package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/daemon"
	"github.com/sarv-projects/pragma/internal/keyvault"
	"github.com/sarv-projects/pragma/internal/pipeline"
	"github.com/sarv-projects/pragma/internal/server"
	"github.com/sarv-projects/pragma/internal/tui"
)

func main() {
	// Check for subcommands before parsing global flags to prevent
	// the global flag parser from interfering with future subcommand-specific flags.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			os.Exit(runDoctor())
		case "upgrade":
			os.Exit(runUpgrade())
		case "setup":
			os.Exit(runSetup())
		case "publish":
			os.Exit(runPublish())
		case "clean":
			os.Exit(runClean())
		}
	}

	headless := flag.Bool("headless", false, "Run in headless mode via stdin")
	useTUI := flag.Bool("tui", false, "Run in terminal UI mode (classic)")
	budgetCap := flag.Float64("budget", 0.0, "Per-run budget cap (overrides config)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("pragma", pragmaCurrentVersion)
		return
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *budgetCap > 0 {
		cfg.Budget.PerRunCap = *budgetCap
	}



	// Inject API keys stored in the OS keyring into the daemon's environment
	// (env vars already set take precedence).
	daemonEnv := keyvault.EnvForDaemon(os.LookupEnv)

	// Inject provider config as environment variables for BYOK support
	daemonEnv = append(daemonEnv,
		"PRAGMA_PROVIDER_NAME="+cfg.Provider.Name,
		"PRAGMA_PROVIDER_BASE_URL="+cfg.Provider.BaseURL,
		"PRAGMA_PROVIDER_REASONING_MODEL="+cfg.Provider.ReasoningModel,
		"PRAGMA_PROVIDER_CODEGEN_MODEL="+cfg.Provider.CodegenModel,
		fmt.Sprintf("PRAGMA_PROVIDER_SUPPORTS_THINKING=%v", cfg.Provider.SupportsThinking),
	)

	if *headless {
		runHeadless(cfg, daemonEnv)
		return
	}

	if *useTUI {
		runTUI(cfg, daemonEnv)
		return
	}

	runWeb(cfg, daemonEnv)
}

func runHeadless(cfg *config.Config, daemonEnv []string) {
	// In headless mode the daemon may log to stderr (no TUI to corrupt).
	dLifecycle := daemon.New(cfg.Daemon.PythonExecutable, daemonEnv, os.Stderr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := dLifecycle.Start(ctx); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
	defer dLifecycle.Stop()

	client, err := daemon.Connect(dLifecycle.SocketPath())
	if err != nil {
		log.Fatalf("Failed to connect to daemon: %v", err)
	}
	defer client.Close()

	oracle := budget.New(cfg.Budget.LifetimeCap, cfg.Budget.PerRunCap, config.BudgetPath())
	oracle.ResetRun()

	events := make(chan pipeline.Event, 100)
	ledger := budget.NewLedger(config.LedgerPath())
	service := pipeline.NewService(client, oracle, cfg, events, ledger)
	service.Headless = true

	manifestBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatalf("Failed to read manifest from stdin: %v", err)
	}

	go func() {
		for ev := range events {
			fmt.Printf("[Event] %T\n", ev)
		}
	}()

	if err := service.StartRun(ctx, string(manifestBytes), cfg.Profile); err != nil {
		log.Fatalf("Run failed: %v", err)
	}
	fmt.Println("Headless run completed successfully.")
}

func runTUI(cfg *config.Config, daemonEnv []string) {
	// CRITICAL: in alt-screen TUI mode, nothing may write to os.Stdout/stderr —
	// it tears the Bubble Tea render. Route all logging to ~/.pragma/pragma.log
	// and the daemon's output to ~/.pragma/daemon.log.
	logFile := openLogFile("pragma.log")
	if logFile != nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	} else {
		log.SetOutput(io.Discard)
	}
	daemonLog := openLogFile("daemon.log")
	var daemonWriter io.Writer = io.Discard
	if daemonLog != nil {
		daemonWriter = daemonLog
		defer daemonLog.Close()
	}

	dLifecycle := daemon.New(cfg.Daemon.PythonExecutable, daemonEnv, daemonWriter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := dLifecycle.Start(ctx); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
	defer dLifecycle.Stop()

	client, err := daemon.Connect(dLifecycle.SocketPath())
	if err != nil {
		log.Fatalf("Failed to connect to daemon: %v", err)
	}
	defer client.Close()

	oracle := budget.New(cfg.Budget.LifetimeCap, cfg.Budget.PerRunCap, config.BudgetPath())
	oracle.ResetRun()

	events := make(chan pipeline.Event, 100)
	ledger := budget.NewLedger(config.LedgerPath())
	service := pipeline.NewService(client, oracle, cfg, events, ledger)

	appModel := tui.NewAppModel(oracle, service, cfg)
	p := tea.NewProgram(appModel, tea.WithAltScreen())

	// Forward pipeline events to the TUI.
	go func() {
		for ev := range events {
			p.Send(ev)
		}
	}()

	// Health monitor (G1): restart the daemon on repeated ping failure; on
	// terminal failure, surface a fatal error to the TUI.
	hm := daemon.NewHealthMonitor(client, dLifecycle, func() {
		p.Send(pipeline.ErrorEvent{Err: fmt.Errorf("daemon unavailable (max restarts exceeded)"), Fatal: true})
	})
	go hm.Start(ctx)

	if _, err := p.Run(); err != nil {
		log.Fatalf("Error starting TUI: %v", err)
	}
}

func openLogFile(name string) *os.File {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".pragma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil
	}
	logPath := filepath.Join(dir, name)

	// Log rotation: if file exceeds 10MB, rename to .old and start fresh
	if info, err := os.Stat(logPath); err == nil && info.Size() > 10*1024*1024 {
		_ = os.Rename(logPath, logPath+".old")
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}
	return f
}

func runWeb(cfg *config.Config, daemonEnv []string) {
	// Detailed logs go to file; user-visible messages go to stdout/stderr.
	logFile := openLogFile("pragma.log")
	if logFile != nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	daemonLog := openLogFile("daemon.log")
	var daemonWriter io.Writer = io.Discard
	if daemonLog != nil {
		daemonWriter = daemonLog
		defer daemonLog.Close()
	}

	// Signal handling: ctrl+c / SIGTERM cleanly shuts down daemon + server.
	// Without this, the daemon becomes an orphan and blocks the next startup.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Determine port
	port := os.Getenv("PRAGMA_PORT")
	if port == "" {
		port = "3777"
	}
	addr := ":" + port
	url := "http://localhost" + addr

	// Always start the web server first — the browser opens immediately.
	// The daemon starts in the background IF a valid key exists. If not,
	// the user sees the Setup Guide and can paste their key there.
	// The daemon starts lazily after a key is saved.
	fmt.Printf("Pragma is running at %s\n", url)
	fmt.Printf("Press Ctrl+C to stop.\n")

	if runtime.GOOS == "linux" && isWSL() {
		fmt.Printf("WSL detected — open your browser manually: %s\n", url)
	} else {
		openBrowser(url)
	}

	// Try to start the daemon. If it fails (no key, bad python path, etc.)
	// we still serve the web UI — the user can fix the issue via Onboarding.
	var service *pipeline.Service
	var client *daemon.Client
	var oracle *budget.Oracle
	var events chan pipeline.Event
	var ledger *budget.Ledger

	dLifecycle := daemon.New(cfg.Daemon.PythonExecutable, daemonEnv, daemonWriter)
	if err := dLifecycle.Start(ctx); err != nil {
		log.Printf("Daemon did not start: %v (server will run without it until key is configured)", err)
	} else {
		defer dLifecycle.Stop()
		if c, err := daemon.Connect(dLifecycle.SocketPath()); err == nil {
			client = c
			defer client.Close()
			oracle = budget.New(cfg.Budget.LifetimeCap, cfg.Budget.PerRunCap, config.BudgetPath())
			oracle.ResetRun()
			events = make(chan pipeline.Event, 100)
			ledger = budget.NewLedger(config.LedgerPath())
			service = pipeline.NewService(client, oracle, cfg, events, ledger)
		} else {
			log.Printf("Failed to connect to daemon: %v", err)
			dLifecycle.Stop()
		}
	}

	if ledger == nil {
		ledger = budget.NewLedger(config.LedgerPath())
	}
	srv := server.New(service, client, cfg, events, oracle, ledger)

	// If daemon didn't start, register the lazy starter so the server can
	// boot the daemon after a key is saved via the Setup Guide.
	if service == nil {
		srv.SetDaemonStarter(func() error {
			return startDaemonAndAttach(ctx, srv, cfg, daemonEnv, daemonWriter)
		})
	}

	// Ensure daemon is stopped when server shuts down (prevents orphan processes)
	defer srv.Shutdown()

	if err := srv.Start(ctx, addr); err != nil {
		if isAddrInUse(err) {
			fmt.Fprintf(os.Stderr, "\nError: port %s is already in use.\n", port)
			fmt.Fprintf(os.Stderr, "Another Pragma instance may be running. Try: pkill -f pragma\n")
			fmt.Fprintf(os.Stderr, "Or set a different port: PRAGMA_PORT=3778 pragma\n")
			os.Exit(1)
		}
		log.Printf("Server stopped: %v", err)
	}

	fmt.Println("\nShutting down...")
}

// startDaemonAndAttach starts the daemon process and attaches it to the server.
// This is called after a key is saved when the server was started without a daemon.
func startDaemonAndAttach(ctx context.Context, srv *server.Server, cfg *config.Config, daemonEnv []string, daemonWriter io.Writer) error {
	// Re-read keys now that one has been saved
	freshEnv := keyvault.EnvForDaemon(os.LookupEnv)
	if len(freshEnv) > 0 {
		daemonEnv = freshEnv
	}

	dLifecycle := daemon.New(cfg.Daemon.PythonExecutable, daemonEnv, daemonWriter)
	if err := dLifecycle.Start(ctx); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	client, err := daemon.Connect(dLifecycle.SocketPath())
	if err != nil {
		dLifecycle.Stop()
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	oracle := budget.New(cfg.Budget.LifetimeCap, cfg.Budget.PerRunCap, config.BudgetPath())
	oracle.ResetRun()

	events := make(chan pipeline.Event, 100)
	ledger := budget.NewLedger(config.LedgerPath())
	service := pipeline.NewService(client, oracle, cfg, events, ledger)

	srv.AttachDaemon(service, client, events, dLifecycle)
	log.Printf("Daemon started and attached after key configuration")
	return nil
}

// openBrowser attempts to open the given URL in the user's default browser.
// It logs a warning if the browser cannot be opened but does not fail.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		log.Printf("Cannot open browser on %s; please visit %s manually", runtime.GOOS, url)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open browser: %v. Please visit %s manually", err, url)
	}
}

// isWSL reports whether the process is running inside Windows Subsystem for Linux.
// On WSL, GOOS is "linux" but xdg-open silently fails, so we print a manual URL instead.
func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

// isAddrInUse reports whether err is a "bind: address already in use" error.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "bind: address already in use") ||
		strings.Contains(msg, "Only one usage of each socket")
}
