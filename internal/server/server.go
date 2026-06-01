package server

import (
	"context"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/daemon"
	"github.com/sarv-projects/pragma/internal/keyvault"
	"github.com/sarv-projects/pragma/internal/pipeline"
	"github.com/sarv-projects/pragma/web"
)

// Server is the HTTP + WebSocket server that serves the SPA and bridges events.
type Server struct {
	mu        sync.RWMutex
	service   *pipeline.Service
	client    *daemon.Client
	config    *config.Config
	hub       *Hub
	events    <-chan pipeline.Event
	mux       *http.ServeMux
	kr        *keyvault.Keyring
	interview *interviewState
	oracle    *budget.Oracle
	ledger    *budget.Ledger

	// daemonLifecycle tracks the daemon process for proper cleanup on shutdown.
	// It's set when the daemon is started (either initially or lazily after key save).
	daemonLifecycle *daemon.Lifecycle

	// daemonStarter is called to start the daemon after a key is saved.
	// It is set by the caller when the server is started without a daemon.
	daemonStarter func() error
	
	// daemonStarting prevents concurrent daemon start attempts
	daemonStarting bool
}

// New creates a Server. The events channel is read by the hub to broadcast to
// WebSocket clients. svc, client, and events may be nil if the daemon has not
// been started yet (no API key configured).
func New(svc *pipeline.Service, client *daemon.Client, cfg *config.Config, events <-chan pipeline.Event, oracle *budget.Oracle, ledger *budget.Ledger) *Server {
	s := &Server{
		service:   svc,
		client:    client,
		config:    cfg,
		hub:       newHub(),
		events:    events,
		mux:       http.NewServeMux(),
		kr:        keyvault.NewKeyring(keyvault.DefaultService),
		interview: &interviewState{},
		oracle:    oracle,
		ledger:    ledger,
	}
	s.routes()
	return s
}

// SetDaemonStarter sets the function called to start the daemon after a key save.
func (s *Server) SetDaemonStarter(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daemonStarter = fn
}

// AttachDaemon attaches a running daemon service and client to the server.
// This is called after the daemon is started post-key-configuration.
// lifecycle is the daemon process manager for proper cleanup on shutdown.
func (s *Server) AttachDaemon(svc *pipeline.Service, client *daemon.Client, events <-chan pipeline.Event, lifecycle *daemon.Lifecycle) {
	s.mu.Lock()
	s.service = svc
	s.client = client
	s.events = events
	s.daemonLifecycle = lifecycle
	s.mu.Unlock()

	// Start forwarding events from the new channel
	go s.forwardEvents()
}

// Shutdown stops the daemon process if it was started by the server.
// This should be called when the server is shutting down to prevent orphan processes.
func (s *Server) Shutdown() {
	// Stop the hub first to close all WebSocket connections gracefully
	if s.hub != nil {
		s.hub.stop()
	}
	
	s.mu.Lock()
	lifecycle := s.daemonLifecycle
	s.mu.Unlock()
	
	if lifecycle != nil {
		log.Printf("Server: stopping daemon process")
		if err := lifecycle.Stop(); err != nil {
			log.Printf("Server: failed to stop daemon: %v", err)
		}
	}
}

// hasDaemon reports whether the daemon is connected.
func (s *Server) hasDaemon() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.service != nil && s.client != nil
}

// routes registers all HTTP handlers.
func (s *Server) routes() {
	// API endpoints
	s.mux.HandleFunc("POST /api/message", s.handleMessage)
	s.mux.HandleFunc("POST /api/approve-spec", s.handleApproveSpec)
	s.mux.HandleFunc("POST /api/approve-dag", s.handleApproveDAG)
	s.mux.HandleFunc("POST /api/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/resume", s.handleResume)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/runs", s.handleListRuns)
	s.mux.HandleFunc("GET /api/download/{run_id}", s.handleDownload)
	s.mux.HandleFunc("POST /api/open-folder", s.handleOpenFolder)
	s.mux.HandleFunc("GET /api/readme", s.handleReadme)
	s.mux.HandleFunc("POST /api/settings", s.handleSaveSettings)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("POST /api/validate-key", s.handleValidateKey)

	// Budget and logs
	s.mux.HandleFunc("GET /api/budget", s.handleBudget)
	s.mux.HandleFunc("GET /api/logs", s.handleLogs)

	s.mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/run-project", s.handleRunProject)
	s.mux.HandleFunc("POST /api/analyze-image", s.handleAnalyzeImage)
	s.mux.HandleFunc("POST /api/select-profile", s.handleSelectProfile)

	// WebSocket
	s.mux.HandleFunc("/ws", s.handleWS)

	// SPA: serve embedded frontend with fallback to index.html
	buildFS, err := fs.Sub(web.BuildFS, "build")
	if err != nil {
		log.Fatalf("server: failed to create sub filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(buildFS))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly first
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		// Check if file exists in the embedded FS
		f, err := buildFS.Open(path[1:]) // strip leading /
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// Start listens on the given address and blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	// Start the hub (broadcasts events to WebSocket clients)
	go s.hub.run()

	// Forward pipeline events to the hub (if events channel exists)
	s.mu.RLock()
	hasEvents := s.events != nil
	s.mu.RUnlock()
	if hasEvents {
		go s.forwardEvents()
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(s.mux),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on context cancellation
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("Pragma web server listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// forwardEvents reads from the pipeline event channel and converts events into
// JSON messages broadcast to all connected WebSocket clients.
func (s *Server) forwardEvents() {
	s.mu.RLock()
	events := s.events
	s.mu.RUnlock()
	if events == nil {
		return
	}
	for ev := range events {
		// Side-effect: record completed runs in the ledger
		if rc, ok := ev.(pipeline.RunCompleteEvent); ok && s.ledger != nil {
			s.ledger.RecordProject(rc.ProjectName, rc.ProjectName, rc.TotalCost)
		}
		msg := eventToJSON(ev)
		if msg != nil {
			s.hub.broadcast <- msg
		}
	}
}

// corsMiddleware rejects POST requests from non-localhost origins.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin != "" && !isLocalhostOrigin(origin) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalhostOrigin checks if the origin is http://localhost:* or http://127.0.0.1:*
func isLocalhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
