package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

//go:generate moq -skip-ensure -out mocks/session_store.go -pkg mocks . SessionStore
//go:generate moq -skip-ensure -out mocks/object_store.go -pkg mocks . ObjectStore

// SessionStore is the catalog persistence the API consumes, satisfied by *store.Store.
// It is exported so the generated mocks package can implement it.
type SessionStore interface {
	Create(ctx context.Context, session store.Session) error
	UpdateManifest(ctx context.Context, session store.Session) (int, error)
	Get(ctx context.Context, id string) (store.Session, error)
	List(ctx context.Context) ([]store.Session, error)
	Delete(ctx context.Context, id string) error
}

// ObjectStore is the R2 blob access the API consumes, satisfied by *blob.Client.
// It is exported so the generated mocks package can implement it.
type ObjectStore interface {
	PresignPut(ctx context.Context, key string) (string, error)
	PresignGet(ctx context.Context, key string) (string, error)
	Exists(ctx context.Context, key string) (bool, error)
}

// Config wires the catalog store, R2 blob client, bearer tokens, and logger into a Server.
type Config struct {
	AdminToken string
	ReadToken  string
	Store      SessionStore
	Blob       ObjectStore
	Logger     *slog.Logger
}

// Server is the catalog HTTP API. Build one with NewServer; the zero value is unusable.
type Server struct {
	store  SessionStore
	blob   ObjectStore
	admin  string
	read   string
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewServer builds a Server from cfg and registers its routes.
func NewServer(cfg Config) *Server {
	s := &Server{
		store:  cfg.Store,
		blob:   cfg.Blob,
		admin:  cfg.AdminToken,
		read:   cfg.ReadToken,
		logger: cfg.Logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler returns the API's HTTP handler with request logging applied.
func (s *Server) Handler() http.Handler {
	return s.logging(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.health)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("encode response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}
