package vault

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	// keysDir is the directory holding device public keys
	// (devices/<id>.pub). Set via WithKeysDir; empty disables
	// signature verification (TEST mode only — production
	// deployments MUST set this). Tier 3 / SR C-1.
	keysDir string

	// chainRoots pins each device's first observed checkpoint
	// hash so VerifyChain can't be tricked by a re-rooted
	// chain. Tier 3 / SR M-12.
	chainRootsMu sync.Mutex
	chainRoots   map[string]string // deviceID → first-checkpoint hash
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
	s := &Server{
		store:      store,
		token:      token,
		chainRoots: make(map[string]string),
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/v1/health", s.handleHealth)
	s.mux.HandleFunc("/v1/search", s.authMiddleware(s.handleSearch))
	s.mux.HandleFunc("/v1/checkpoints", s.authMiddleware(s.handleCheckpoints))
	return s
}

// WithKeysDir wires the directory holding device public keys.
// Tier 3 / SR C-1: when set, handleCheckpoints verifies each
// incoming checkpoint's ed25519 signature against the device's
// pub-key file at <keysDir>/<deviceID>.pub. Empty keysDir
// disables verification (TEST mode only; production MUST set).
func (s *Server) WithKeysDir(dir string) *Server {
	s.keysDir = dir
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

// maxVaultRequestBytes caps the request body for /v1/search and
// /v1/checkpoints (Tier 3 / SR C-6). Generous enough for the
// largest expected checkpoint (8 KiB summary + 32 KiB
// FilesTouched + 32 KiB embedding floats) with headroom.
const maxVaultRequestBytes = 4 * 1024 * 1024

// maxSearchEmbedding caps the embedding length (in floats)
// accepted by /v1/search (Tier 3 / SR C-6). 4096 matches the
// largest production embedder dimensions × 8 byte slack.
const maxSearchEmbedding = 4096

// maxTopK caps the search result count.
const maxTopK = 100

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Tier 3 / SR C-6: cap request body before decoder allocates.
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultRequestBytes)

	var req struct {
		Embedding []float32 `json:"embedding"`
		Repo      string    `json:"repo"`
		TopK      int       `json:"top_k"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Embedding) > maxSearchEmbedding {
		http.Error(w, "embedding too long", http.StatusBadRequest)
		return
	}
	if req.TopK == 0 {
		req.TopK = 5
	}
	if req.TopK > maxTopK {
		req.TopK = maxTopK
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
	// Tier 3 / SR C-6: cap request body.
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultRequestBytes)

	var req struct {
		Checkpoint memory.Checkpoint `json:"checkpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	cp := &req.Checkpoint

	// Verify hash integrity — recompute and compare.
	computed := memory.CanonicalHash(cp)
	if computed != cp.Hash {
		http.Error(w, "hash mismatch", http.StatusForbidden)
		return
	}

	// Tier 3 / SR C-1: ed25519 verification BEFORE persist. Without
	// this the vault accepted any checkpoint with a recomputed
	// hash — combined with C-2 (Summary→system-prompt) that was
	// cross-session prompt injection. When keysDir is empty the
	// server is in TEST mode and skips verification with a
	// recorded warning so deployments can detect misconfiguration.
	if s.keysDir != "" {
		if err := s.verifyCheckpointSignature(cp); err != nil {
			http.Error(w, "signature verification failed", http.StatusForbidden)
			if s.logger != nil {
				s.logger.Printf("vault: signature verify %s: %v", cp.Hash, err)
			}
			return
		}
		// Tier 3 / SR M-12: pin per-device chain root on first
		// observed checkpoint; reject subsequent attempts to
		// re-root the chain.
		if err := s.checkChainRoot(cp); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			if s.logger != nil {
				s.logger.Printf("vault: chain root %s: %v", cp.DeviceID, err)
			}
			return
		}
	} else if s.logger != nil {
		s.logger.Printf("vault: SR C-1 WARNING — signature verification DISABLED (set WithKeysDir for production)")
	}

	// Store (idempotent via INSERT OR IGNORE)
	if err := s.store.Append(cp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// verifyCheckpointSignature loads the device's public key from
// <keysDir>/<deviceID>.pub and verifies cp.Signature over cp.Hash.
// Tier 3 / SR C-1.
func (s *Server) verifyCheckpointSignature(cp *memory.Checkpoint) error {
	if cp.DeviceID == "" {
		return errors.New("empty device id")
	}
	// Tier 3 / SR C-3: refuse DeviceIDs with path-traversal
	// characters before joining into a filesystem path.
	if strings.ContainsAny(cp.DeviceID, "/\\") || strings.Contains(cp.DeviceID, "..") {
		return errors.New("device id contains path-traversal characters")
	}
	pubPath := filepath.Join(s.keysDir, cp.DeviceID+".pub")
	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return errors.New("unknown device key")
	}
	pub, err := memory.UnmarshalPublicKey(pubBytes)
	if err != nil {
		return err
	}
	sigBytes, err := hex.DecodeString(cp.Signature)
	if err != nil {
		return errors.New("malformed signature encoding")
	}
	if !ed25519.Verify(pub, []byte(cp.Hash), sigBytes) {
		return errors.New("signature verification failed")
	}
	return nil
}

// checkChainRoot enforces SR M-12: the FIRST checkpoint observed
// for each deviceID pins that device's chain root; subsequent
// attempts to submit a checkpoint whose ParentHash=zero hash
// claim "first" status are refused.
func (s *Server) checkChainRoot(cp *memory.Checkpoint) error {
	s.chainRootsMu.Lock()
	defer s.chainRootsMu.Unlock()
	if cp.ParentHash == "" || cp.ParentHash == strings.Repeat("0", 64) {
		// Claims to be a chain root.
		if existing, ok := s.chainRoots[cp.DeviceID]; ok {
			if existing != cp.Hash {
				return errors.New("chain root already pinned for device")
			}
		} else {
			s.chainRoots[cp.DeviceID] = cp.Hash
		}
	}
	return nil
}
