package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Lifecycle struct {
	cmd       *exec.Cmd
	sockPath  string
	sockDir   string
	pythonExe string
	extraEnv  []string
	logWriter io.Writer
	logFile   *os.File
	ctx       context.Context
	cancel    context.CancelFunc
	waitDone  chan error // signals when Wait() completes
}

// New creates a daemon lifecycle manager.
func New(pythonExe string, extraEnv []string, logWriter io.Writer) *Lifecycle {
	// Use /tmp/pragma/ for the socket directory to avoid path length
	// issues (Unix sockets have a 104-char limit). On Windows, use TempDir.
	var sockDir string
	if runtime.GOOS == "windows" {
		sockDir = filepath.Join(os.TempDir(), "pragma")
	} else {
		sockDir = "/tmp/pragma"
	}
	_ = os.MkdirAll(sockDir, 0700)

	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	sockName := fmt.Sprintf("daemon-%d-%s.sock", os.Getpid(), hex.EncodeToString(suffix))
	sockPath := filepath.Join(sockDir, sockName)

	// Ensure total socket path is under 104 chars (Unix domain socket limit)
	if len(sockPath) >= 104 {
		sockName = fmt.Sprintf("d-%s.sock", hex.EncodeToString(suffix))
		sockPath = filepath.Join(sockDir, sockName)
	}

	return &Lifecycle{
		sockPath:  sockPath,
		sockDir:   sockDir,
		pythonExe: pythonExe,
		extraEnv:  extraEnv,
		logWriter: logWriter,
	}
}

func defaultLogWriter() (io.Writer, *os.File) {
	home, err := os.UserHomeDir()
	if err != nil {
		return io.Discard, nil
	}
	dir := filepath.Join(home, ".pragma")
	_ = os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return io.Discard, nil
	}
	return f, f
}

func (l *Lifecycle) Start(ctx context.Context) error {
	// CRITICAL: Clean up any zombie daemon processes and stale sockets from
	// previous runs that were killed without a clean shutdown (ctrl+c in
	// terminal, system crash, OOM kill, etc.). Without this, the new daemon
	// either can't bind the socket or two daemons fight over requests.
	l.cleanupStaleProcesses()

	l.ctx, l.cancel = context.WithCancel(context.Background())
	_ = os.Remove(l.sockPath)

	args := []string{"-m", "pragma_daemon", "--socket", l.sockPath}

	l.cmd = exec.CommandContext(l.ctx, l.pythonExe, args...)
	l.cmd.Env = append(os.Environ(), l.extraEnv...)

	w := l.logWriter
	if w == nil {
		w, l.logFile = defaultLogWriter()
	}
	l.cmd.Stdout = w
	l.cmd.Stderr = w

	if err := l.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Start a goroutine to wait for process exit and detect early failures
	l.waitDone = make(chan error, 1)
	go func() {
		l.waitDone <- l.cmd.Wait()
	}()

	// Write a PID file so future startups can find and kill this process
	// if it becomes orphaned.
	l.writePIDFile()

	// Wait for socket file to appear
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = l.Stop()
			return ctx.Err()
		case <-timeout:
			_ = l.Stop()
			return fmt.Errorf("timeout waiting for daemon socket to appear at %s", l.sockPath)
		case err := <-l.waitDone:
			// Process exited early (bad python path, import error, etc.)
			if err != nil {
				return fmt.Errorf("daemon process exited prematurely: %w. Check ~/.pragma/daemon.log", err)
			}
			return fmt.Errorf("daemon process exited prematurely. Check ~/.pragma/daemon.log")
		case <-ticker.C:
			if _, err := os.Stat(l.sockPath); err == nil {
				return nil
			}
		}
	}
}

func (l *Lifecycle) Stop() error {
	if l.cancel != nil {
		l.cancel()
	}
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- l.cmd.Wait() }()
		select {
		case <-time.After(5 * time.Second):
			_ = l.cmd.Process.Kill()
		case <-done:
		}
	}
	_ = os.Remove(l.sockPath)
	l.removePIDFile()
	if l.logFile != nil {
		_ = l.logFile.Close()
		l.logFile = nil
	}
	return nil
}

func (l *Lifecycle) Restart(ctx context.Context) error {
	_ = l.Stop()
	return l.Start(ctx)
}

func (l *Lifecycle) SocketPath() string {
	return l.sockPath
}

// cleanupStaleProcesses kills any orphaned pragma_daemon processes from
// previous runs and removes their stale socket files. This is what makes
// "just run pragma again" work without manual pkill.
func (l *Lifecycle) cleanupStaleProcesses() {
	// 1. Kill any process recorded in a stale PID file.
	pidFile := l.pidFilePath()
	if data, err := os.ReadFile(pidFile); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pidStr != "" {
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil && pid > 0 {
				if proc, err := os.FindProcess(pid); err == nil {
					// Check if the process is still alive (signal 0 doesn't kill)
					if err := proc.Signal(syscall.Signal(0)); err == nil {
						if err := proc.Signal(os.Interrupt); err != nil {
							log.Printf("daemon: failed to send interrupt to stale process %d: %v", pid, err)
						}
						time.Sleep(200 * time.Millisecond)
						if err := proc.Signal(syscall.SIGKILL); err != nil {
							log.Printf("daemon: failed to send SIGKILL to stale process %d: %v", pid, err)
						}
					}
				}
			}
		}
		_ = os.Remove(pidFile)
	}

	// 2. Remove stale socket files (sockets whose owning PID no longer exists).
	entries, err := os.ReadDir(l.sockDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		// Extract PID from socket name: daemon-<PID>-<random>.sock
		parts := strings.Split(e.Name(), "-")
		if len(parts) < 3 || parts[0] != "daemon" {
			continue
		}
		var sockPID int
		if _, err := fmt.Sscanf(parts[1], "%d", &sockPID); err != nil {
			continue
		}
		// If the process that owns this socket is dead, remove the socket.
		if !processAlive(sockPID) {
			_ = os.Remove(filepath.Join(l.sockDir, e.Name()))
		}
	}
}

func (l *Lifecycle) pidFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(l.sockDir, "daemon.pid")
	}
	return filepath.Join(home, ".pragma", "daemon.pid")
}

func (l *Lifecycle) writePIDFile() {
	if l.cmd == nil || l.cmd.Process == nil {
		return
	}
	pidFile := l.pidFilePath()
	_ = os.MkdirAll(filepath.Dir(pidFile), 0755)
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", l.cmd.Process.Pid)), 0644)
}

func (l *Lifecycle) removePIDFile() {
	_ = os.Remove(l.pidFilePath())
}


