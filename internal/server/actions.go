package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     CheckOrigin,
}

// CheckOrigin validates that the WebSocket request originates from localhost.
func CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Allow connections with no Origin header (e.g., CLI tools)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// handleWS upgrades the connection to WebSocket and registers the client.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("server: websocket upgrade failed: %v", err)
		return
	}

	client := &wsClient{
		hub:  s.hub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	s.hub.register <- client

	go client.writePump()
	go client.readPump(s.dispatchAction)
}

// dispatchAction handles incoming WebSocket messages from the browser.
func (s *Server) dispatchAction(raw []byte) {
	var msg struct {
		Action  string `json:"action"`
		Content string `json:"content"`
		RunID   string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("server: invalid action message: %v", err)
		return
	}

	switch msg.Action {
	case "send_message":
		go s.handleInterviewMessage(msg.Content)
	case "approve_spec":
		if s.hasDaemon() {
			s.service.ApproveSpec()
		}
	case "reject_spec":
		// Rejecting spec resets interview state so user can start fresh
		s.ResetInterview()
		s.broadcastError("Spec rejected. Please start a new project.", false)
	case "approve_dag":
		if s.hasDaemon() {
			s.service.ApproveDAG()
		}
	case "reject_dag":
		s.ResetInterview()
		s.broadcastError("DAG rejected. Please start a new project.", false)
	case "update_manifest":
		s.handleUpdateManifest(raw)
	case "pause_run":
		// Pause is not yet implemented in the pipeline service
		s.broadcastError("Pause is not yet supported. The pipeline will continue running.", false)
	case "resume_run":
		go s.resumeRun(msg.RunID)
	case "refine_spec":
		go func() {
			if err := s.service.RefineSpec(context.Background(), msg.Content); err != nil {
				s.broadcastError(fmt.Sprintf("Failed to refine spec: %v", err), false)
			}
		}()
	case "extend_project":
		go s.handleExtendProject(msg.RunID, msg.Content)
	default:
		log.Printf("server: unknown action: %s", msg.Action)
	}
}

func (s *Server) broadcastError(message string, fatal bool) {
	msg, _ := json.Marshal(map[string]any{
		"type":    "error",
		"message": message,
		"fatal":   fatal,
	})
	s.hub.broadcast <- msg
}

// handleUpdateManifest updates the interview state's manifest with data from
// the PreCompileView (project name, additions) before the pipeline starts.
func (s *Server) handleUpdateManifest(raw []byte) {
	var payload struct {
		Action      string `json:"action"`
		ProjectName string `json:"project_name"`
		Additions   string `json:"additions"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		log.Printf("server: invalid update_manifest payload: %v", err)
		return
	}

	// Store the updated manifest data in the interview state so that
	// startPipelineRun can pick it up when the interview completes.
	s.interview.mu.Lock()
	s.interview.manifestUpdate = &manifestUpdate{
		ProjectName: payload.ProjectName,
		Additions:   payload.Additions,
	}
	ch := s.interview.manifestUpdateCh
	s.interview.mu.Unlock()

	// Re-run profile router with additions to update the tech stack if needed
	if payload.Additions != "" {
		combinedDesc := payload.Additions
		newProfile := config.SelectProfile(combinedDesc)
		if newProfile != s.config.Profile {
			s.config.Profile = newProfile
			if saveErr := s.config.Save(config.DefaultPath()); saveErr != nil {
				log.Printf("server: failed to save profile from additions: %v", saveErr)
			}
			profileMsg, _ := json.Marshal(map[string]any{
				"type":    "profile_chosen",
				"profile": newProfile,
			})
			s.hub.broadcast <- profileMsg
			log.Printf("server: profile updated to %q from additions", newProfile)
		}
	}

	// Signal that the manifest update has arrived
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	log.Printf("server: manifest updated (project_name=%q)", payload.ProjectName)
}

func (s *Server) resumeRun(runID string) {
	if !s.hasDaemon() {
		s.broadcastError("Cannot resume: daemon is not running. Please configure an API key first.", false)
		return
	}
	if runID == "" {
		s.broadcastError("Cannot resume: no run ID provided", false)
		return
	}

	state, err := pipeline.LoadCheckpoint(runID, s.config.Output.Directory)
	if err != nil {
		log.Printf("server: failed to load checkpoint for run %s: %v", runID, err)
		s.broadcastError("Cannot resume: run not found or checkpoint corrupted", false)
		return
	}

	// Use a timeout context instead of context.Background() to prevent leaks on shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.service.Resume(ctx, *state); err != nil {
		log.Printf("server: resume failed for run %s: %v", runID, err)
		s.hub.broadcast <- eventToJSON(pipeline.ErrorEvent{Err: err, Fatal: true})
	}
}

func (s *Server) handleExtendProject(runID, content string) {
	if !s.hasDaemon() {
		s.broadcastError("Cannot extend project: daemon is not running.", false)
		return
	}
	if runID == "" {
		s.broadcastError("Cannot extend project: no run ID provided", false)
		return
	}

	state, err := pipeline.LoadCheckpoint(runID, s.config.Output.Directory)
	if err != nil {
		log.Printf("server: failed to load checkpoint for extend_project run %s: %v", runID, err)
		s.broadcastError("Cannot extend project: run not found or checkpoint corrupted", false)
		return
	}

	// Call extend_project RPC on the daemon
	args := map[string]any{
		"checkpoint_manifest": state.Manifest,
		"checkpoint_spec":     state.Spec,
		"new_requirements":    content,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	result, err := client.Call(ctx, "extend_project", args)
	if err != nil {
		log.Printf("server: extend_project RPC failed: %v", err)
		s.broadcastError("Failed to extend project: "+err.Error(), false)
		return
	}

	// Broadcast the delta spec to the frontend (new format includes impact analysis)
	msg, _ := json.Marshal(map[string]any{
		"type":       "extend_project_ready",
		"delta_spec": json.RawMessage(result),
		"run_id":     runID,
	})
	s.hub.broadcast <- msg
	log.Printf("server: extend_project completed for run %s", runID)

	// Also store the result in the run directory for the HTTP endpoint
	resultPath := filepath.Join(s.config.Output.Directory, runID, ".extend_project_result.json")
	_ = os.WriteFile(resultPath, result, 0644)
}

// handleExtendProjectHTTP serves the extend_project RPC via HTTP for the RefineView UI.
func (s *Server) handleExtendProjectHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hasDaemon() {
		writeError(w, http.StatusServiceUnavailable, "daemon not running")
		return
	}

	var req struct {
		CheckpointManifest map[string]any `json:"checkpoint_manifest"`
		CheckpointSpec     map[string]any `json:"checkpoint_spec"`
		NewRequirements    string         `json:"new_requirements"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NewRequirements == "" {
		writeError(w, http.StatusBadRequest, "new_requirements is required")
		return
	}

	args := map[string]any{
		"checkpoint_manifest": req.CheckpointManifest,
		"checkpoint_spec":     req.CheckpointSpec,
		"new_requirements":    req.NewRequirements,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	result, err := client.Call(ctx, "extend_project", args)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "extend_project RPC failed: "+err.Error())
		return
	}

	// Parse the response (which now includes impact + delta)
	var responseData map[string]any
	if err := json.Unmarshal(result, &responseData); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse extend_project response")
		return
	}

	writeJSON(w, http.StatusOK, responseData)
}

// handleApplyDelta applies the approved delta spec to the run directory.
func (s *Server) handleApplyDelta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID     string         `json:"run_id"`
		DeltaSpec map[string]any `json:"delta_spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RunID == "" || req.DeltaSpec == nil {
		writeError(w, http.StatusBadRequest, "run_id and delta_spec are required")
		return
	}
	// Validate runID: no path traversal
	if strings.Contains(req.RunID, "..") || strings.Contains(req.RunID, "/") || strings.Contains(req.RunID, "\\") {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	// Load the pipeline service for the run to apply the delta
	state, err := pipeline.LoadCheckpoint(req.RunID, s.config.Output.Directory)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	// Use the daemon's apply_delta RPC if it exists, otherwise fall back to manual file application
	deltaSpecJSON, _ := json.Marshal(req.DeltaSpec)
	args := map[string]any{
		"run_id":     req.RunID,
		"output_dir": filepath.Join(s.config.Output.Directory, req.RunID),
		"delta_spec": json.RawMessage(deltaSpecJSON),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	result, err := client.Call(ctx, "apply_delta", args)
	if err != nil {
		// If apply_delta RPC doesn't exist, do a simple manual application
		log.Printf("server: apply_delta RPC not available, falling back to manual file writing: %v", err)
		// Manually write the files from the delta spec
		if files, ok := req.DeltaSpec["files"].([]any); ok {
			outputDir := filepath.Join(s.config.Output.Directory, req.RunID)
			outputDirAbs, _ := filepath.Abs(outputDir)
			for _, f := range files {
				fileMap, ok := f.(map[string]any)
				if !ok {
					continue
				}
				pathVal, ok := fileMap["path"].(string)
				if !ok || pathVal == "" {
					continue
				}
				// SECURITY: validate path doesn't escape the output directory (prevent path traversal)
				fullPath := filepath.Join(outputDir, pathVal)
				fullPathAbs, _ := filepath.Abs(fullPath)
				if !strings.HasPrefix(fullPathAbs, outputDirAbs) {
					log.Printf("server: blocked path traversal attempt: %s", pathVal)
					continue
				}
				content := ""
				if c, ok := fileMap["content"].(string); ok {
					content = c
				}
				if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err == nil {
					_ = os.WriteFile(fullPath, []byte(content), 0644)
				}
			}
		}
		// Update the checkpoint spec in state (marshalling map to json.RawMessage)
		deltaSpecBytes, _ := json.Marshal(req.DeltaSpec)
		state.Spec = deltaSpecBytes
		_ = pipeline.SaveCheckpoint(*state, s.config.Output.Directory)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "updated_spec": state.Spec})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "result": string(result)})
}
