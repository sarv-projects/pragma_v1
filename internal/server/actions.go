package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
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

	ctx := context.Background()
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

	// Broadcast the delta spec to the frontend
	msg, _ := json.Marshal(map[string]any{
		"type":       "extend_project_ready",
		"delta_spec": json.RawMessage(result),
		"run_id":     runID,
	})
	s.hub.broadcast <- msg
	log.Printf("server: extend_project completed for run %s", runID)
}
