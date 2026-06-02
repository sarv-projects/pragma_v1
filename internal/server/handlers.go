package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/keyvault"
	"github.com/sarv-projects/pragma/internal/pipeline"
	"archive/zip"
	"os/exec"
	"runtime"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running; please configure an API key first")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	// Route to interview handler
	go s.handleInterviewMessage(req.Content)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) handleApproveSpec(w http.ResponseWriter, _ *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running")
		return
	}
	s.service.ApproveSpec()
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) handleApproveDAG(w http.ResponseWriter, _ *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running")
		return
	}
	s.service.ApproveDAG()
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) handlePause(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "not_implemented",
		"message": "Pause is not yet supported. The pipeline will continue running.",
	})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running; please configure an API key first")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Load checkpoint and resume
	state, err := pipeline.LoadCheckpoint(req.RunID, s.config.Output.Directory)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found or cannot be resumed")
		return
	}

	go func() {
		if err := s.service.Resume(context.Background(), *state); err != nil {
			s.hub.broadcast <- eventToJSON(pipeline.ErrorEvent{Err: err, Fatal: true})
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "resuming"})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	if !s.hasDaemon() {
		writeJSON(w, http.StatusOK, map[string]any{
			"phase":           "setup",
			"project_name":    "",
			"files_completed": 0,
			"total_files":     0,
		})
		return
	}
	state := s.service.State()
	writeJSON(w, http.StatusOK, map[string]any{
		"phase":                phaseToWire(state.Phase),
		"project_name":         state.ProjectName,
		"files_completed":      len(state.FilesCompleted),
		"total_files":          len(state.FilesCompleted) + len(state.FilesRemaining),
		"files_completed_list": state.FilesCompleted,
	})
}

func (s *Server) handleListRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := pipeline.ListRuns(s.config.Output.Directory)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	runDir := filepath.Join(s.config.Output.Directory, runID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+runID+".zip\"")

	zw := zip.NewWriter(w)
	defer zw.Close()

	_ = filepath.Walk(runDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}

		zf, err := zw.Create(rel)
		if err != nil {
			f.Close()
			return err
		}

		_, _ = io.Copy(zf, f)
		f.Close()
		return nil
	})
}

func (s *Server) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Security: validate path is within output directory to prevent path traversal
	outputDir := s.config.Output.Directory
	if outputDir == "" {
		outputDir = "./output"
	}
	
	// Clean and resolve paths
	cleanPath := filepath.Clean(req.Path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	
	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid output directory")
		return
	}
	
	// Ensure the requested path is within the output directory
	rel, err := filepath.Rel(absOutputDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		writeError(w, http.StatusForbidden, "path must be within output directory")
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", absPath)
	case "darwin":
		cmd = exec.Command("open", absPath)
	default:
		cmd = exec.Command("xdg-open", absPath)
	}

	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open folder")
		return
	}

	// Just detach
	go func() {
		_ = cmd.Wait()
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
}

func (s *Server) handleReadme(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	readmePath := filepath.Join(s.config.Output.Directory, runID, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "README.md not found")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}


func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Provider   string `json:"provider"`
		APIKey     string `json:"api_key"`
		GroqAPIKey string `json:"groq_api_key"`
		BaseURL    string `json:"base_url"`
		Mode       string `json:"mode"`
		Profile    string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Save the API key to the keyring based on provider
	keyName := providerToKeyName(req.Provider)
	if keyName != "" && req.APIKey != "" {
		if err := s.kr.Set(keyName, req.APIKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save key")
			return
		}
	}

	// Save Groq key if provided (sent from Setup Guide alongside the primary key)
	if req.GroqAPIKey != "" {
		if err := s.kr.Set(keyvault.KeyGroq, req.GroqAPIKey); err != nil {
			log.Printf("server: failed to save groq key: %v", err)
		}
	}

	// Validate the key after saving
	validated := false
	if req.APIKey != "" {
		baseURL := req.BaseURL
		if baseURL == "" {
			switch strings.ToLower(req.Provider) {
			case "deepseek":
				baseURL = "https://api.deepseek.com"
			case "openai":
				baseURL = "https://api.openai.com/v1"
			case "groq":
				baseURL = "https://api.groq.com/openai/v1"
			case "together":
				baseURL = "https://api.together.xyz/v1"
			}
		}
		_, valErr := ValidateAPIKey(baseURL, req.APIKey, req.Provider)
		validated = valErr == nil
	}

	// Update mode in config if provided
	if req.Mode != "" {
		s.config.Mode = req.Mode
		if err := s.config.Save(config.DefaultPath()); err != nil {
			log.Printf("server: failed to save config: %v", err)
		}
	}

	// Update profile in config if provided
	if req.Profile != "" {
		s.config.Profile = req.Profile
		if err := s.config.Save(config.DefaultPath()); err != nil {
			log.Printf("server: failed to save profile config: %v", err)
		}
	}

	// If daemon is not running and we just saved a key, try to start it
	if !s.hasDaemon() && req.APIKey != "" {
		s.mu.Lock()
		// Check if daemon start is already in progress to prevent race condition
		if s.daemonStarting {
			s.mu.Unlock()
		} else {
			s.daemonStarting = true
			starter := s.daemonStarter
			s.mu.Unlock()
			
			if starter != nil {
				go func() {
					defer func() {
						s.mu.Lock()
						s.daemonStarting = false
						s.mu.Unlock()
					}()
					if err := starter(); err != nil {
						log.Printf("server: failed to start daemon after key save: %v", err)
					}
				}()
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "validated": validated})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	// Return settings with masked keys
	settings := map[string]any{
		"mode":    s.config.Mode,
		"profile": s.config.Profile,
		"keys":    getKeyStatus(s.kr),
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleBudget(w http.ResponseWriter, _ *http.Request) {
	result := map[string]any{}
	if s.oracle != nil {
		st := s.oracle.Status()
		result["mode"] = st.Mode
		result["lifetime_cap"] = st.LifetimeCap
		result["per_run_cap"] = st.PerRunCap
		result["total_spent"] = st.TotalSpent
		result["run_spent"] = st.RunSpent
		result["runs_complete"] = st.RunsComplete
	}
	if s.ledger != nil {
		for k, v := range s.ledger.Summary() {
			result[k] = v
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLogs(w http.ResponseWriter, _ *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	logPath := filepath.Join(home, ".pragma", "daemon.log")

	f, err := os.Open(logPath)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	defer f.Close()

	// Tail approach: read at most the last 64KB
	const maxRead = 64 * 1024
	const maxLines = 50

	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}

	size := info.Size()
	readSize := size
	if readSize > maxRead {
		readSize = maxRead
	}

	buf := make([]byte, readSize)
	offset := size - readSize
	if offset < 0 {
		offset = 0
	}
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	buf = buf[:n]

	// Find the last maxLines newlines
	content := string(buf)
	lines := strings.Split(content, "\n")

	// If we seeked into the middle of the file, discard the first partial line
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}

	// Take the last maxLines lines
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	// Filter empty trailing line
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// Return as plain text (newline-separated) so the frontend can display it directly
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}

// getKeyStatus returns which providers have keys configured (without exposing values).
func getKeyStatus(kr *keyvault.Keyring) map[string]any {
	keys := map[string]any{}
	for _, name := range []string{keyvault.KeyDeepSeek, keyvault.KeyGroq, keyvault.KeyCustom} {
		v, err := kr.Get(name)
		if err == nil && v != "" {
			// Mask: show first 4 and last 4 chars
			masked := maskKey(v)
			keys[name] = map[string]any{"configured": true, "masked": masked}
		} else {
			keys[name] = map[string]any{"configured": false, "masked": ""}
		}
	}
	return keys
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// handleAnalyzeImage sends an image to the daemon's analyze_image RPC (Groq Scout).
// Images are NEVER sent to DeepSeek — Groq is required for this feature.
func (s *Server) handleAnalyzeImage(w http.ResponseWriter, r *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running; configure an API key first")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	// Groq key is required — vision uses llama-4-scout, not DeepSeek
	groqKey, err := s.kr.Get(keyvault.KeyGroq)
	if err != nil || groqKey == "" {
		writeError(w, http.StatusBadRequest, "Groq API key required for image analysis. Add a Groq key in Settings to enable this feature.")
		return
	}

	var req struct {
		ImageBase64 string `json:"image_base64"`
		Mode        string `json:"mode"` // ui | document | diagram
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ImageBase64 == "" {
		writeError(w, http.StatusBadRequest, "image_base64 is required")
		return
	}
	// base64 of 4MB image ≈ 5.5MB string
	if len(req.ImageBase64) > 6*1024*1024 {
		writeError(w, http.StatusBadRequest, "image too large (max 4 MB)")
		return
	}
	if req.Mode == "" {
		req.Mode = "ui"
	}

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	raw, err := client.Call(ctx, "analyze_image", map[string]any{
		"image_base64": req.ImageBase64,
		"mode":         req.Mode,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image analysis failed: "+err.Error())
		return
	}

	// Re-marshal the raw result so we can introspect token usage
	resultBytes, _ := json.Marshal(raw)
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		writeJSON(w, http.StatusOK, raw)
		return
	}

	// Record approximate cost in ledger (Groq Scout: ~$0.11/M in + $0.34/M out)
	if s.ledger != nil {
		if tokensUsed, ok := result["tokens_used"].(float64); ok && tokensUsed > 0 {
			approxCost := tokensUsed * 0.34 / 1_000_000 // conservative output-token rate
			s.ledger.RecordProject("vision_"+req.Mode, "Image Analysis (Groq Scout)", approxCost)
		}
	}

	// Store structured fields in interview state so they get merged into the
	// manifest before StartRun — not only as text in the description textarea.
	s.interview.mu.Lock()
	s.interview.analysisResult = result
	s.interview.mu.Unlock()

	writeJSON(w, http.StatusOK, result)
}

func providerToKeyName(provider string) string {
	switch strings.ToLower(provider) {
	case "deepseek":
		return keyvault.KeyDeepSeek
	case "groq":
		return keyvault.KeyGroq
	case "custom":
		return keyvault.KeyCustom
	default:
		return keyvault.KeyDeepSeek
	}
}

// handleSelectProfile auto-picks a build profile from the user's project description.
func (s *Server) handleSelectProfile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	profile := config.SelectProfile(req.Text)

	// Persist to config so it's used when the pipeline starts
	s.config.Profile = profile
	if err := s.config.Save(config.DefaultPath()); err != nil {
		log.Printf("server: failed to save selected profile: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"profile": profile,
	})
}

// handleProfiles returns the list of available build profiles with friendly metadata.
func (s *Server) handleProfiles(w http.ResponseWriter, _ *http.Request) {
	names := config.ProfileNames()
	type ProfileInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		BeginnerLabel string `json:"beginner_label,omitempty"`
		Language      string `json:"language"`
	}
	var profiles []ProfileInfo
	for _, name := range names {
		p, err := config.LoadProfile(name)
		if err != nil {
			continue
		}
		profiles = append(profiles, ProfileInfo{
			ID:            name,
			Name:          p.Meta.Name,
			Description:   p.Meta.Description,
			BeginnerLabel: p.Meta.BeginnerLabel,
			Language:      p.Meta.Language,
		})
	}
	if profiles == nil {
		profiles = []ProfileInfo{}
	}
	writeJSON(w, http.StatusOK, profiles)
}

// handleHealth returns a structured health report for the in-app health panel.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	type Check struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}

	checks := map[string]Check{}

	// Python check — verify the configured executable exists
	pythonExe := s.config.Daemon.PythonExecutable
	if pythonExe == "" {
		pythonExe = "python3"
	}
	if _, err := exec.LookPath(pythonExe); err != nil {
		// Also check venv
		home, _ := os.UserHomeDir()
		venvPy := filepath.Join(home, ".pragma", "venv", "bin", "python")
		if runtime.GOOS == "windows" {
			venvPy = filepath.Join(home, ".pragma", "venv", "Scripts", "python.exe")
		}
		if _, err2 := os.Stat(venvPy); err2 == nil {
			checks["python"] = Check{OK: true, Message: "Python found in ~/.pragma/venv"}
		} else {
			checks["python"] = Check{OK: false, Message: "Python not found. Run: pragma setup"}
		}
	} else {
		checks["python"] = Check{OK: true, Message: "Python available: " + pythonExe}
	}

	// Daemon check
	if s.hasDaemon() {
		checks["daemon"] = Check{OK: true, Message: "Daemon running"}
	} else {
		checks["daemon"] = Check{OK: false, Message: "Daemon not running. Check python is installed and run: pragma setup"}
	}

	// DeepSeek key
	if dsKey, err := s.kr.Get(keyvault.KeyDeepSeek); err == nil && dsKey != "" {
		checks["deepseek_key"] = Check{OK: true, Message: "Configured"}
	} else {
		checks["deepseek_key"] = Check{OK: false, Message: "Not configured — required for code generation"}
	}

	// Groq key (required — image analysis, faster chat & healing)
	if groqKey, err := s.kr.Get(keyvault.KeyGroq); err == nil && groqKey != "" {
		checks["groq_key"] = Check{OK: true, Message: "Configured (image analysis, faster chat & healing)"}
	} else {
		checks["groq_key"] = Check{OK: false, Message: "Not configured — required for image analysis, also accelerates chat & healing"}
	}

	// Docker check
	dockerCtx, dockerCancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer dockerCancel()
	dockerCmd := exec.CommandContext(dockerCtx, "docker", "version", "--format", "{{.Server.Version}}")
	dockerOut, dockerErr := dockerCmd.Output()
	if dockerErr != nil {
		checks["docker"] = Check{OK: false, Message: "Docker not found — install Docker Desktop to run generated apps"}
	} else {
		checks["docker"] = Check{OK: true, Message: "Docker " + strings.TrimSpace(string(dockerOut))}
	}

	// WSL detection
	isWSL := false
	if data, err := os.ReadFile("/proc/version"); err == nil {
		lower := strings.ToLower(string(data))
		isWSL = strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
	}

	// Port
	port := os.Getenv("PRAGMA_PORT")
	if port == "" {
		port = "3777"
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"checks": checks,
		"is_wsl": isWSL,
		"port":   port,
		"all_ok": allOK,
	})
}

// handleRunProject runs `docker compose up -d` in the project output directory.
func (s *Server) handleRunProject(w http.ResponseWriter, r *http.Request) {
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

	runDir := filepath.Join(s.config.Output.Directory, req.RunID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "project directory not found")
		return
	}

	// Check docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Docker not found. Install Docker Desktop from https://www.docker.com/products/docker-desktop/")
		return
	}

	// Check compose file exists
	composePath := filepath.Join(runDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		composePath = filepath.Join(runDir, "compose.yml")
		if _, err := os.Stat(composePath); os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "No docker-compose.yml found in project directory")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "up", "-d", "--build")
	cmd.Dir = runDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "docker compose failed: "+string(out))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "started",
		"output": string(out),
	})
}
