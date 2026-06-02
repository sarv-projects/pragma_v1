package server

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

// Hub manages WebSocket clients and broadcasts messages.
type Hub struct {
	clients    map[*wsClient]struct{}
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	slowClient chan *wsClient // buffered channel for slow clients to avoid deadlock
	done       chan struct{}
	mu         sync.RWMutex
}

type wsClient struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	closed atomic.Bool
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*wsClient]struct{}),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		slowClient: make(chan *wsClient, 100), // buffered to avoid blocking broadcast
		done:       make(chan struct{}),
	}
}

func (h *Hub) run() {
	for {
		select {
		case <-h.done:
			// Shutdown signal received, close all client connections
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
			
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			clientCount := len(h.clients)
			h.mu.Unlock()

			// Warn if multiple tabs are connected
			if clientCount > 1 {
				warnMsg, _ := json.Marshal(map[string]any{
					"type":    "warning",
					"message": "Multiple tabs detected. Use only one tab.",
				})
				h.broadcast <- warnMsg
			}

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.closed.Store(true)
				close(client.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				if client.closed.Load() {
					continue
				}
				select {
				case client.send <- msg:
				default:
					// Client too slow, drop it via buffered channel to avoid deadlock
					select {
					case h.slowClient <- client:
					default:
						// slowClient channel full, just skip this client
					}
				}
			}
			h.mu.RUnlock()
			
		case client := <-h.slowClient:
			// Drain slow clients and unregister them
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.closed.Store(true)
				close(client.send)
			}
			h.mu.Unlock()
		}
	}
}

// stop signals the hub to shut down gracefully
func (h *Hub) stop() {
	close(h.done)
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) readPump(actionHandler func([]byte)) {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(64 * 1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Rate limiting: max 10 messages per second per client
	const rateLimit = 10
	const rateWindow = time.Second
	var msgCount int
	windowStart := time.Now()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Simple rate limiter: count messages in a 1-second window
		now := time.Now()
		if now.Sub(windowStart) > rateWindow {
			msgCount = 0
			windowStart = now
		}
		msgCount++
		if msgCount >= rateLimit {
			log.Printf("server: rate limit exceeded for client, closing connection")
			_ = c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"))
			break
		}

		actionHandler(message)
	}
}

// eventToJSON converts a pipeline event to a JSON byte slice matching the wire
// format expected by the WebSocket store (ws.ts).
func eventToJSON(ev pipeline.Event) []byte {
	var msg any

	switch e := ev.(type) {
	case pipeline.PhaseChangedEvent:
		msg = map[string]any{
			"type": "phase_changed",
			"from": phaseToWire(e.From),
			"to":   phaseToWire(e.To),
		}
	case pipeline.InterviewMessageEvent:
		// This is an internal event for TUI; interview responses are sent
		// directly via sendInterviewResponse. Skip here.
		return nil
	case pipeline.SpecReadyEvent:
		wireData := map[string]any{
			"type":       "spec_ready",
			"spec":       json.RawMessage(e.Spec),
			"file_count": e.FileCount,
			"test_count": e.TestCount,
		}
		// Include a plain-English summary if present in the spec
		var specWithSummary struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(e.Spec, &specWithSummary); err == nil && specWithSummary.Summary != "" {
			wireData["summary"] = specWithSummary.Summary
		}
		msg = wireData
	case pipeline.DAGReadyEvent:
		msg = map[string]any{
			"type":        "dag_ready",
			"slices":      e.Slices,
			"est_seconds": e.EstSeconds,
			"est_cost":    e.EstCost,
		}
	case pipeline.FileCompletedEvent:
		msg = map[string]any{
			"type":        "file_completed",
			"path":        e.Path,
			"healed":      e.Healed,
			"failed":      e.Failed,
			"duration_ms": e.Duration.Milliseconds(),
			"description": e.Description,
		}
	case pipeline.BudgetUpdatedEvent:
		msg = map[string]any{
			"type":      "budget_updated",
			"run_spent": e.Status.RunSpent,
			"remaining": e.Status.LifetimeCap - e.Status.TotalSpent,
		}
	case pipeline.RunCompleteEvent:
		msg = map[string]any{
			"type":         "run_complete",
			"output_path":  e.OutputPath,
			"file_count":   e.FileCount,
			"healed":       e.Healed,
			"failed":       e.Failed,
			"cost":         e.TotalCost,
			"budget_left":  e.BudgetLeft,
			"coverage":     e.Coverage,
			"project_name": e.ProjectName,
		}
	case pipeline.ErrorEvent:
		msg = map[string]any{
			"type":    "error",
			"message": e.Err.Error(),
			"fatal":   e.Fatal,
		}
	case pipeline.LogEvent:
		msg = map[string]any{
			"type":    "log",
			"level":   e.Level,
			"message": e.Message,
		}
	case pipeline.CoverageReportEvent:
		// Not needed on the wire; the coverage info is in run_complete
		return nil
	case pipeline.SecurityAuditEvent:
		msg = map[string]any{
			"type":     "security_audit",
			"warnings": e.Warnings,
		}
	case pipeline.SpecAmendmentProposedEvent:
		msg = map[string]any{
			"type":      "spec_amendment_proposed",
			"file_path": e.FilePath,
			"reason":    e.Reason,
		}
	case pipeline.TestRunEvent:
		msg = map[string]any{
			"type":    "test_run",
			"command": e.Command,
			"passed":  e.Passed,
			"output":  e.Output,
		}
	default:
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("server: failed to marshal event: %v", err)
		return nil
	}
	return data
}

// phaseToWire converts a pipeline.Phase to the wire string used by the frontend.
func phaseToWire(p pipeline.Phase) string {
	switch p {
	case pipeline.PhaseIdeation:
		return "ideation"
	case pipeline.PhaseResearching:
		return "researching"
	case pipeline.PhaseCompilingSpec:
		return "compiling_spec"
	case pipeline.PhaseSpecReview:
		return "spec_review"
	case pipeline.PhaseDAGReview:
		return "dag_review"
	case pipeline.PhaseGenerating:
		return "generating"
	case pipeline.PhaseComplete:
		return "complete"
	case pipeline.PhasePaused:
		return "paused"
	case pipeline.PhaseFailed:
		return "error"
	default:
		return "idle"
	}
}
