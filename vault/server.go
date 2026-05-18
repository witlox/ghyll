package vault

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/memory"
)

// Server is the ghyll-vault HTTP server for team memory search.
type Server struct {
	store          *memory.Store
	engine         *engine.Store
	engineAttached bool
	logger         vaultLogger
	token          string
	mux            *http.ServeMux
}

// vaultLogger is the minimal logger surface the v2 endpoints use.
// Indirected so cmd/ghyll-vault can inject a structured logger
// without an import cycle.
type vaultLogger interface {
	Printf(format string, v ...any)
}

// WithLogger sets the logger used by the v2 endpoints to record
// internal errors (V3) and encode failures (V14). Nil disables
// logging (default).
func (s *Server) WithLogger(l vaultLogger) *Server {
	s.logger = l
	return s
}

// NewServer creates a vault server.
func NewServer(store *memory.Store, token string) *Server {
	s := &Server{store: store, token: token}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/v1/health", s.handleHealth)
	s.mux.HandleFunc("/v1/search", s.authMiddleware(s.handleSearch))
	s.mux.HandleFunc("/v1/checkpoints", s.authMiddleware(s.handleCheckpoints))
	return s
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// V10: case-insensitive scheme + constant-time token compare.
		if s.token != "" {
			auth := r.Header.Get("Authorization")
			scheme, rest, hasSpace := strings.Cut(auth, " ")
			if !hasSpace || !strings.EqualFold(scheme, "Bearer") ||
				subtle.ConstantTimeCompare([]byte(rest), []byte(s.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Embedding []float32 `json:"embedding"`
		Repo      string    `json:"repo"`
		TopK      int       `json:"top_k"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.TopK == 0 {
		req.TopK = 5
	}

	results, err := s.store.SearchByEmbedding(req.Embedding, req.Repo, req.TopK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"results": results,
	})
}

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Checkpoint memory.Checkpoint `json:"checkpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	cp := &req.Checkpoint

	// Verify hash integrity — recompute and compare
	computed := memory.CanonicalHash(cp)
	if computed != cp.Hash {
		http.Error(w, "hash mismatch", http.StatusForbidden)
		return
	}

	// Store (idempotent via INSERT OR IGNORE)
	if err := s.store.Append(cp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}
