package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kernelhub/internal/commands"
)

const apiV1Prefix = "/api/v1"

type Config struct {
	ListenAddr string
	DBPath     string

	RateLimit RateLimitConfig
}

func DefaultConfig() Config {
	return Config{
		ListenAddr: "127.0.0.1:8080",
		DBPath:     "./workspace/history.db",
		RateLimit:  DefaultRateLimitConfig(),
	}
}

type Server struct {
	cfg       Config
	rl        *RateLimiter
	httpSrv   *http.Server
	startedAt time.Time
}

func New(cfg Config) *Server {
	return &Server{
		cfg: cfg,
		rl:  NewRateLimiter(cfg.RateLimit),
	}
}

// ListenAndServe starts the HTTP server. It blocks until the server is
// shut down or an error occurs.
func (s *Server) ListenAndServe() error {
	if strings.TrimSpace(s.cfg.DBPath) == "" {
		return fmt.Errorf("db-path cannot be empty")
	}

	addr := strings.TrimSpace(s.cfg.ListenAddr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	handler := s.rl.Wrap(mux)

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.startedAt = time.Now()

	display := addr
	if strings.HasPrefix(addr, ":") {
		display = "127.0.0.1" + addr
	}
	fmt.Printf("[kernelhub server] listening on http://%s\n", display)

	return s.httpSrv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleRoot)

	mux.HandleFunc(apiV1Prefix+"/snapshot", s.handleSnapshot)
	mux.HandleFunc(apiV1Prefix+"/patch", s.handlePatch)
	mux.HandleFunc(apiV1Prefix+"/runs", s.handleRunsIngest)
	mux.HandleFunc(apiV1Prefix+"/iterations", s.handleIterationsIngest)
	mux.HandleFunc(apiV1Prefix+"/archives", s.handleArchivesIngest)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":    "kernelhub",
		"version":    "0.1.0",
		"api_prefix": apiV1Prefix,
		"started_at": s.startedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	includePatches := parseBool(r.URL.Query().Get("include_patches"))
	snapshot, err := commands.BuildSnapshot(s.cfg.DBPath, includePatches)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	q := r.URL.Query()
	commit := strings.TrimSpace(q.Get("commit"))
	if commit == "" {
		writeError(w, http.StatusBadRequest, "commit query param is required")
		return
	}
	repoPath := strings.TrimSpace(q.Get("repo_path"))
	if repoPath == "" {
		writeError(w, http.StatusBadRequest, "repo_path query param is required")
		return
	}
	parent := strings.TrimSpace(q.Get("parent"))
	patch, patchErr := commands.BuildCommitPatch(repoPath, commit, parent)
	if patchErr != "" {
		writeJSON(w, http.StatusOK, map[string]string{"error": patchErr})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"patch": patch})
}

// ---------------------------------------------------------------------------
// Helpers (re-used from commands package via thin delegation)
// ---------------------------------------------------------------------------

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
