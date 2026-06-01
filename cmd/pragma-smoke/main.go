// Command pragma-smoke performs an end-to-end cross-process smoke test:
// it starts the Python daemon, connects over the Unix socket, and issues a
// real ping. This catches the class of runtime errors (import failures, RPC
// wiring, cache/groq/conformance API mismatches) that a plain `go build`
// cannot — which is why green CI was previously misleading.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sarv-projects/pragma/internal/daemon"
)

func main() {
	python := findPython()
	fmt.Printf("smoke: using python = %s\n", python)

	// Provide a dummy key so the daemon boots. Model discovery will fail
	// against the dummy key and fall back to the hardcoded list — that's fine,
	// the point is to exercise startup + RPC, not the network.
	env := []string{}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		env = append(env, "DEEPSEEK_API_KEY=smoke-dummy-key")
	}

	lc := daemon.New(python, env, os.Stderr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := lc.Start(ctx); err != nil {
		fail("daemon failed to start: %v", err)
	}
	defer lc.Stop()
	fmt.Println("smoke: daemon started")

	client, err := daemon.Connect(lc.SocketPath())
	if err != nil {
		fail("failed to connect: %v", err)
	}
	defer client.Close()
	fmt.Println("smoke: connected to socket")

	pingCtx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	defer pcancel()
	res, err := client.Call(pingCtx, "ping", nil)
	if err != nil {
		fail("ping failed: %v", err)
	}

	var pong string
	if err := json.Unmarshal(res, &pong); err != nil {
		fail("could not decode ping result: %v", err)
	}
	if pong != "pong" {
		fail("unexpected ping result: %q", pong)
	}

	fmt.Println("smoke: ping -> pong ✓")
	fmt.Println("SMOKE OK")
}

func findPython() string {
	if p := os.Getenv("PRAGMA_PYTHON"); p != "" {
		return p
	}
	// Prefer a local venv if present (CI creates one).
	for _, cand := range []string{".venv/bin/python3", ".venv/bin/python"} {
		if abs, err := filepath.Abs(cand); err == nil {
			if _, statErr := os.Stat(abs); statErr == nil {
				return abs
			}
		}
	}
	for _, cand := range []string{"python3.12", "python3.11", "python3"} {
		if path, err := exec.LookPath(cand); err == nil {
			return path
		}
	}
	return "python3"
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "SMOKE FAIL: "+format+"\n", args...)
	os.Exit(1)
}
