package server

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/sarv-projects/pragma/internal/config"
)

// interviewState tracks the conversation for the current interview session.
type interviewState struct {
	mu               sync.Mutex
	messages         []map[string]string
	manifestUpdate   *manifestUpdate
	pendingManifest  interface{}
	manifestUpdateCh chan struct{}
	inFlight         bool // guards against concurrent RPC calls

	// analysisResult holds structured fields from image/vision analysis (Groq Scout).
	// Merged into the manifest before StartRun so endpoints, data_models, and
	// integrations appear as structured JSON — not only as text in the description.
	analysisResult map[string]any
}

// manifestUpdate holds updates from the PreCompileView.
type manifestUpdate struct {
	ProjectName string
	Additions   string
}

// handleInterviewMessage sends a user message to the daemon interview_chat RPC
// and broadcasts the response as an interview_response WebSocket event.
func (s *Server) handleInterviewMessage(content string) {
	if !s.hasDaemon() {
		s.broadcastError("Cannot send message: no API key configured. Please add a key in Settings.", false)
		return
	}

	// Debounce: only one RPC in flight at a time per session.
	s.interview.mu.Lock()
	if s.interview.inFlight {
		s.interview.mu.Unlock()
		return // silently drop the duplicate — the UI will show the first response
	}
	s.interview.inFlight = true
	s.interview.messages = append(s.interview.messages, map[string]string{
		"role":    "user",
		"content": content,
	})
	msgs := make([]map[string]string, len(s.interview.messages))
	copy(msgs, s.interview.messages)
	s.interview.mu.Unlock()

	defer func() {
		s.interview.mu.Lock()
		s.interview.inFlight = false
		s.interview.mu.Unlock()
	}()

	// Call daemon's interview_chat method
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	args := map[string]any{
		"messages": msgs,
	}

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	result, err := client.Call(ctx, "interview_chat", args)
	if err != nil {
		log.Printf("server: interview_chat RPC failed: %v", err)
		// Provide user-friendly error based on the failure type
		errMsg := "Interview service temporarily unavailable. Please try again."
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "Response timed out. The AI is taking too long. Please try again."
		}
		s.broadcastError(errMsg, false)
		return
	}

	var resp struct {
		Content  string      `json:"content"`
		Done     bool        `json:"done"`
		Manifest interface{} `json:"manifest"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		log.Printf("server: failed to parse interview response: %v", err)
		s.broadcastError("Failed to parse interview response", false)
		return
	}

	// Add assistant response to conversation history
	s.interview.mu.Lock()
	s.interview.messages = append(s.interview.messages, map[string]string{
		"role":    "assistant",
		"content": resp.Content,
	})
	s.interview.mu.Unlock()

	// Broadcast to WebSocket clients
	wireMsg := map[string]any{
		"type":     "interview_response",
		"content":  resp.Content,
		"done":     resp.Done,
		"manifest": resp.Manifest,
	}
	data, _ := json.Marshal(wireMsg)
	s.hub.broadcast <- data

	// If interview is done, store the manifest and wait for the PreCompileView
	// to send update_manifest before starting the pipeline. This avoids a race
	// where the pipeline starts before the user can name the project.
	if resp.Done && resp.Manifest != nil {
		s.interview.mu.Lock()
		s.interview.pendingManifest = resp.Manifest
		s.interview.mu.Unlock()

		// Re-run the profile router with the refined manifest description so the
		// selected tech stack reflects the full interview context, not just the
		// initial one-liner.  Broadcast the result so PreCompileView shows it.
		if manifestBytes, err := json.Marshal(resp.Manifest); err == nil {
			var manifestObj map[string]interface{}
			if err := json.Unmarshal(manifestBytes, &manifestObj); err == nil {
				if desc, ok := manifestObj["description"].(string); ok && desc != "" {
					newProfile := config.SelectProfile(desc)
					s.config.Profile = newProfile
					if saveErr := s.config.Save(config.DefaultPath()); saveErr != nil {
						log.Printf("server: failed to save re-selected profile: %v", saveErr)
					}
					profileMsg, _ := json.Marshal(map[string]any{
						"type":    "profile_chosen",
						"profile": newProfile,
					})
					s.hub.broadcast <- profileMsg
					log.Printf("server: profile re-selected to %q from manifest description", newProfile)
				}
			}
		}

		// Start a goroutine that waits for the manifest update (or times out)
		go s.waitForManifestAndStart()
	}
}

// startPipelineRun kicks off the pipeline with the interview manifest.
func (s *Server) startPipelineRun(manifest interface{}) {
	if !s.hasDaemon() {
		s.broadcastError("Cannot start pipeline: daemon is not connected", true)
		return
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		log.Printf("server: failed to marshal manifest: %v", err)
		s.broadcastError("Failed to start pipeline: invalid manifest", true)
		return
	}

	// Apply any manifest updates from PreCompileView
	s.interview.mu.Lock()
	update := s.interview.manifestUpdate
	s.interview.manifestUpdate = nil
	s.interview.mu.Unlock()

	if update != nil {
		var m map[string]interface{}
		if err := json.Unmarshal(manifestBytes, &m); err == nil {
			if update.ProjectName != "" {
				m["project_name"] = update.ProjectName
			}
			if update.Additions != "" {
				desc, _ := m["description"].(string)
				m["description"] = desc + "\n\nAdditional notes: " + update.Additions
			}
			if desc, ok := m["description"].(string); ok && desc != "" {
				newProfile := config.SelectProfile(desc)
				if newProfile != s.config.Profile {
					s.config.Profile = newProfile
					if saveErr := s.config.Save(config.DefaultPath()); saveErr != nil {
						log.Printf("server: failed to save profile from PreCompile additions: %v", saveErr)
					}
					log.Printf("server: profile re-selected to %q from PreCompile additions", newProfile)
				}
			}
			if updated, err := json.Marshal(m); err == nil {
				manifestBytes = updated
			}
		}
	}

	// Merge structured fields from vision analysis (Groq Scout) into the manifest.
	// If the interview already produced these fields, we keep them — the analysis
	// result only fills fields that are empty or missing from the manifest.
	s.interview.mu.Lock()
	vision := s.interview.analysisResult
	s.interview.analysisResult = nil
	s.interview.mu.Unlock()

	if vision != nil {
		var m map[string]interface{}
		if err := json.Unmarshal(manifestBytes, &m); err == nil {
			for _, field := range []string{"endpoints", "data_models", "integrations"} {
				if existing, ok := m[field].([]interface{}); !ok || len(existing) == 0 {
					if v, ok := vision[field]; ok {
						m[field] = v
					}
				}
			}
			if updated, err := json.Marshal(m); err == nil {
				manifestBytes = updated
			}
		}
	}

	ctx := context.Background()
	if err := s.service.StartRun(ctx, string(manifestBytes), s.config.Profile); err != nil {
		log.Printf("server: pipeline run failed: %v", err)
		// Error events are already emitted by the service
	}
}

// ResetInterview clears the interview conversation state for a new project.
func (s *Server) ResetInterview() {
	s.interview.mu.Lock()
	s.interview.messages = nil
	s.interview.manifestUpdate = nil
	s.interview.pendingManifest = nil
	s.interview.manifestUpdateCh = nil
	s.interview.inFlight = false
	s.interview.analysisResult = nil
	s.interview.mu.Unlock()
}

// waitForManifestAndStart waits for the PreCompileView to send an update_manifest
// action (up to 10 seconds), then starts the pipeline with the final manifest.
func (s *Server) waitForManifestAndStart() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("server: panic in waitForManifestAndStart: %v", r)
			s.broadcastError("Internal error starting pipeline. Please try again.", true)
		}
	}()
	s.interview.mu.Lock()
	ch := make(chan struct{}, 1)
	s.interview.manifestUpdateCh = ch
	s.interview.mu.Unlock()

	// Wait for the manifest update signal or timeout
	select {
	case <-ch:
		// PreCompileView sent update_manifest
	case <-time.After(30 * time.Second):
		// Timeout — proceed with the original manifest
		log.Printf("server: manifest update timeout (30s), proceeding with original manifest")
	}

	s.interview.mu.Lock()
	manifest := s.interview.pendingManifest
	s.interview.pendingManifest = nil
	s.interview.manifestUpdateCh = nil
	s.interview.mu.Unlock()

	if manifest != nil {
		s.startPipelineRun(manifest)
	}
}
