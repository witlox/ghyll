package vault

import (
	"encoding/json"
	"net/http"

	"github.com/witlox/ghyll/memory"
)

// Server is the ghyll-vault HTTP server for team memory search.
type Server struct {
	store *memory.Store
	token string
	mux   *http.ServeMux
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
		// If a token is configured, always require it.
		// Invariant 26: localhost needs no token — but only when no token is configured.
		if s.token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+s.token {
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
