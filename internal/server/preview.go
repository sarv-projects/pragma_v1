package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// previewState tracks a running dev server for a project.
type previewState struct {
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	port      int
	runID     string
	startedAt time.Time
}

var (
	activePreview     *previewState
	activePreviewMu   sync.Mutex
	previewPortOffset = 0
)

// handleStartPreview starts a dev server for the generated project and returns
// the preview URL. The dev server command is determined by the project's
// package.json or docker-compose.yml.
func (s *Server) handleStartPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RunID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	// Path traversal check
	if strings.Contains(req.RunID, "..") || strings.Contains(req.RunID, "/") || strings.Contains(req.RunID, "\\") {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	runDir := filepath.Join(s.config.Output.Directory, req.RunID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "project directory not found")
		return
	}

	// Stop any existing preview
	activePreviewMu.Lock()
	if activePreview != nil {
		activePreview.cancel()
		if activePreview.cmd != nil && activePreview.cmd.Process != nil {
			_ = activePreview.cmd.Process.Kill()
		}
		activePreview = nil
	}
	activePreviewMu.Unlock()

	// Determine how to start the dev server based on project files
	cmd, port, err := s.detectAndStartDevServer(runDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start dev server: "+err.Error())
		return
	}

	cancelFunc := context.CancelFunc(func() {})

	if err := cmd.Start(); err != nil {
		_ = cancelFunc
		writeError(w, http.StatusInternalServerError, "failed to start dev server: "+err.Error())
		return
	}

	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}

	preview := &previewState{
		cmd:       cmd,
		cancel:    cancelFunc,
		port:      port,
		runID:     req.RunID,
		startedAt: time.Now(),
	}

	activePreviewMu.Lock()
	activePreview = preview
	activePreviewMu.Unlock()

	// Wait a moment for the server to start
	time.Sleep(2 * time.Second)

	previewURL := fmt.Sprintf("http://localhost:%d", port)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "started",
		"preview_url": previewURL,
		"port":        port,
	})

	log.Printf("server: preview started for %s on port %d", req.RunID, port)
}

// handleStopPreview stops the running dev server.
func (s *Server) handleStopPreview(w http.ResponseWriter, _ *http.Request) {
	activePreviewMu.Lock()
	defer activePreviewMu.Unlock()

	if activePreview == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_preview"})
		return
	}

	activePreview.cancel()
	if activePreview.cmd != nil && activePreview.cmd.Process != nil {
		_ = activePreview.cmd.Process.Kill()
	}
	log.Printf("server: preview stopped for %s", activePreview.runID)
	activePreview = nil
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// handlePreviewStatus returns the current preview state.
func (s *Server) handlePreviewStatus(w http.ResponseWriter, _ *http.Request) {
	activePreviewMu.Lock()
	defer activePreviewMu.Unlock()

	if activePreview == nil {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active":       true,
		"run_id":       activePreview.runID,
		"port":         activePreview.port,
		"preview_url":  fmt.Sprintf("http://localhost:%d", activePreview.port),
		"started_at":   activePreview.startedAt,
	})
}

// handlePreviewProxy proxies requests to the running dev server.
func (s *Server) handlePreviewProxy(w http.ResponseWriter, r *http.Request) {
	activePreviewMu.Lock()
	preview := activePreview
	activePreviewMu.Unlock()

	if preview == nil {
		writeError(w, http.StatusServiceUnavailable, "no preview running")
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://localhost:%d", preview.port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ServeHTTP(w, r)
}

// detectAndStartDevServer determines how to start the dev server for a
// generated project based on its files.
func (s *Server) detectAndStartDevServer(runDir string) (*exec.Cmd, int, error) {
	// Pick an available port starting from 4000
	port := 4000 + previewPortOffset
	previewPortOffset++

	// Check for package.json (Node.js project)
	if _, err := os.Stat(filepath.Join(runDir, "package.json")); err == nil {
		// Check if node_modules exists
		if _, err := os.Stat(filepath.Join(runDir, "node_modules")); os.IsNotExist(err) {
			// Install dependencies first
			installCmd := exec.Command("npm", "install")
			installCmd.Dir = runDir
			installCmd.Env = append(os.Environ(), "NODE_ENV=development")
			out, err := installCmd.CombinedOutput()
			if err != nil {
				return nil, 0, fmt.Errorf("npm install failed: %s", string(out))
			}
		}

		// Try npm run dev, then npm start
		cmd := exec.Command("npm", "run", "dev", "--", "--port", fmt.Sprintf("%d", port), "--host", "0.0.0.0")
		cmd.Dir = runDir
		cmd.Env = append(os.Environ(),
			"NODE_ENV=development",
			fmt.Sprintf("PORT=%d", port),
		)
		return cmd, port, nil
	}

	// Check for requirements.txt or pyproject.toml (Python project)
	if _, err := os.Stat(filepath.Join(runDir, "requirements.txt")); err == nil {
		// Check for main.py or app.py
		mainFile := ""
		for _, name := range []string{"main.py", "app.py", "server.py"} {
			if _, err := os.Stat(filepath.Join(runDir, name)); err == nil {
				mainFile = name
				break
			}
		}
		if mainFile != "" {
			cmd := exec.Command("python3", mainFile)
			cmd.Dir = runDir
			cmd.Env = append(os.Environ(),
				fmt.Sprintf("PORT=%d", port),
				"HOST=0.0.0.0",
			)
			return cmd, port, nil
		}
	}

	// Check for docker-compose.yml
	for _, name := range []string{"docker-compose.yml", "compose.yml"} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err == nil {
			cmd := exec.Command("docker", "compose", "up", "-d", "--build")
			cmd.Dir = runDir
			// For Docker, we use the first exposed port
			return cmd, port, nil
		}
	}

	return nil, 0, fmt.Errorf("no supported project type found (no package.json, requirements.txt, or docker-compose.yml)")
}
