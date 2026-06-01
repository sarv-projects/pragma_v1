package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/daemon"
	"github.com/sarv-projects/pragma/internal/keyvault"
)

// pragmaCurrentVersion is overridden at release build time via -ldflags.
var pragmaCurrentVersion = "dev"

func runDoctor() int {
	fmt.Println("Running Pragma Doctor...")
	failures := 0

	// Config (C5: never nil-deref — fall back to defaults).
	cfg, err := config.Load(config.DefaultPath())
	if err != nil || cfg == nil {
		fmt.Printf("⚠️  Config could not be loaded cleanly (%v); using defaults\n", err)
		cfg, _ = config.Load(os.DevNull) // returns defaults
		if cfg == nil {
			cfg = &config.Config{Mode: config.ModeFast, Daemon: config.DaemonConfig{PythonExecutable: "python3"}}
		}
	} else {
		fmt.Printf("✅ Configuration loaded (mode: %s)\n", cfg.Mode)
	}


	// 1. Python availability + version >= 3.11
	if ok := checkPython(cfg.Daemon.PythonExecutable); !ok {
		failures++
	}

	if checkKeys() != true {
		failures++
	}

	checkReachable("DeepSeek API", "https://api.deepseek.com")
	checkReachable("DeepWiki MCP", "https://mcp.deepwiki.com")

	if ok := checkDocker(); !ok {
		// Docker is optional — don't increment failures, just warn
		fmt.Println("⚠️  Docker not found. Install Docker Desktop to run generated apps.")
	}

	// 4. Daemon start + ping (exercises cross-process RPC).
	daemonEnv := keyvault.EnvForDaemon(os.LookupEnv)
	dLifecycle := daemon.New(cfg.Daemon.PythonExecutable, daemonEnv, os.Stderr)
	ctx := context.Background()

	fmt.Print("Starting daemon process... ")
	if err := dLifecycle.Start(ctx); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		failures++
	} else {
		defer dLifecycle.Stop()
		fmt.Println("✅")

		fmt.Print("Connecting to socket... ")
		client, err := daemon.Connect(dLifecycle.SocketPath())
		if err != nil {
			fmt.Printf("❌ Failed: %v\n", err)
			failures++
		} else {
			defer client.Close()
			fmt.Println("✅")

			fmt.Print("Pinging daemon... ")
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if _, err := client.Call(pingCtx, "ping", nil); err != nil {
				fmt.Printf("❌ Failed: %v\n", err)
				failures++
			} else {
				fmt.Println("✅")
			}
		}
	}

	fmt.Println()
	if failures == 0 {
		fmt.Println("All checks passed. Pragma is ready to build!")
		return 0
	}
	fmt.Printf("%d check(s) failed. See messages above.\n", failures)
	return 1
}

func checkPython(pythonExe string) bool {
	if pythonExe == "" {
		pythonExe = "python3"
	}
	out, err := exec.Command(pythonExe, "--version").CombinedOutput()
	if err != nil {
		fmt.Printf("❌ Python (%s) not available: %v\n", pythonExe, err)
		return false
	}
	verStr := strings.TrimSpace(string(out))
	// Expect "Python 3.x.y"
	fields := strings.Fields(verStr)
	if len(fields) >= 2 {
		parts := strings.Split(fields[1], ".")
		if len(parts) >= 2 {
			major, _ := strconv.Atoi(parts[0])
			minor, _ := strconv.Atoi(parts[1])
			if major > 3 || (major == 3 && minor >= 11) {
				fmt.Printf("✅ %s (>= 3.11)\n", verStr)
				return true
			}
			fmt.Printf("❌ %s — Python 3.11+ required\n", verStr)
			return false
		}
	}
	fmt.Printf("⚠️  Could not parse Python version: %q\n", verStr)
	return false
}

func checkKeys() bool {
	if hasKey("DEEPSEEK_API_KEY", keyvault.KeyDeepSeek) {
		fmt.Println("✅ DeepSeek key present (fast mode)")
		return true
	}
	fmt.Println("❌ Fast mode needs a DeepSeek key (DEEPSEEK_API_KEY or keyring).")
	return false
}

func hasKey(envName, keyringName string) bool {
	if v := os.Getenv(envName); v != "" {
		return true
	}
	kr := keyvault.NewKeyring(keyvault.DefaultService)
	if v, err := kr.Get(keyringName); err == nil && v != "" {
		return true
	}
	return false
}

func checkDocker() bool {
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		fmt.Printf("⚠️  Docker: not found (install Docker Desktop to run generated projects)\n")
		return false
	}
	fmt.Printf("✅ Docker %s\n", strings.TrimSpace(string(out)))
	return true
}

func checkReachable(label, url string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		// Some endpoints reject HEAD; try GET as a fallback.
		resp, err = client.Get(url)
	}
	if err != nil {
		fmt.Printf("⚠️  %s not reachable: %v\n", label, err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("✅ %s reachable (HTTP %d)\n", label, resp.StatusCode)
}
