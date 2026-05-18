package vault

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/witlox/ghyll/engine"
)

// v2 endpoints surface the structured-query side of the persistence
// engine. Per phase-9 design: v2 entities (findings, amendments,
// classifications, grid arrows, evaluation runs) have explicit
// identity, so the right access pattern is filter+sort+paginate —
// NOT the embedding-similarity search the v1 checkpoint endpoint
// provides.
//
// All endpoints share the same auth model as v1 (Bearer token).
// Read-only: the vault server doesn't write v2 entities; the
// ghyll session loop's engine.Journal writes them, the vault just
// serves the read side.

// AttachEngine registers the v2 endpoints on the existing mux. The
// engine.Store is read-only for the vault — write paths live in
// the session-loop layer.
func (s *Server) AttachEngine(store *engine.Store) {
	s.engine = store
	s.mux.HandleFunc("/v2/findings", s.authMiddleware(s.handleFindings))
	s.mux.HandleFunc("/v2/findings/transitions", s.authMiddleware(s.handleTransitions))
	s.mux.HandleFunc("/v2/amendments", s.authMiddleware(s.handleAmendments))
	s.mux.HandleFunc("/v2/arrows", s.authMiddleware(s.handleArrows))
	s.mux.HandleFunc("/v2/requirements", s.authMiddleware(s.handleRequirements))
	s.mux.HandleFunc("/v2/classifications", s.authMiddleware(s.handleClassifications))
	s.mux.HandleFunc("/v2/evaluation-runs", s.authMiddleware(s.handleEvaluationRuns))
}

// queryInt parses an int from the query string, defaulting to def.
// Negative or unparseable values fall back to def.
func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// queryBool parses an optional bool ("true" / "false"). Returns
// nil for absent so the engine.AmendmentFilter can distinguish
// "either" from "false."
func queryBool(r *http.Request, key string) *bool {
	raw := r.URL.Query().Get(key)
	switch raw {
	case "true", "1":
		t := true
		return &t
	case "false", "0":
		f := false
		return &f
	}
	return nil
}

// writeJSON serializes v as JSON with the appropriate header.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeServerError returns 500 with the error message body. The
// vault is operator-facing; verbose errors are acceptable.
func writeServerError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// handleFindings: GET /v2/findings?arrow_id=...&status=...&min_severity=...&type=...&limit=...&offset=...
func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	filter := engine.FindingFilter{
		ArrowID:     r.URL.Query().Get("arrow_id"),
		Status:      r.URL.Query().Get("status"),
		MinSeverity: queryInt(r, "min_severity", -1),
		Type:        r.URL.Query().Get("type"),
		Limit:       queryInt(r, "limit", 100),
		Offset:      queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListFindings(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"findings": rows})
}

// handleTransitions: GET /v2/findings/transitions?finding_id=...&limit=...
func (s *Server) handleTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	findingID := r.URL.Query().Get("finding_id")
	if findingID == "" {
		http.Error(w, "finding_id required", http.StatusBadRequest)
		return
	}
	rows, err := s.engine.ListTransitions(r.Context(), findingID, queryInt(r, "limit", 100))
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"transitions": rows})
}

// handleAmendments: GET /v2/amendments?source_arrow=...&drained=true|false&limit=...&offset=...
func (s *Server) handleAmendments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	filter := engine.AmendmentFilter{
		SourceArrow: r.URL.Query().Get("source_arrow"),
		Drained:     queryBool(r, "drained"),
		Limit:       queryInt(r, "limit", 100),
		Offset:      queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListAmendments(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"amendments": rows})
}

// handleArrows: GET /v2/arrows?kind=append|on-the-spot&min_grid_version=...&limit=...&offset=...
func (s *Server) handleArrows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	minGrid := queryInt(r, "min_grid_version", 0)
	if minGrid < 0 {
		minGrid = 0
	}
	filter := engine.ArrowFilter{
		Kind:       r.URL.Query().Get("kind"),
		MinGridVer: uint64(minGrid),
		Limit:      queryInt(r, "limit", 100),
		Offset:     queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListArrows(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"arrows": rows})
}

// handleRequirements: GET /v2/requirements?arrow_id=...&limit=...
func (s *Server) handleRequirements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	filter := engine.RequirementFilter{
		ArrowID: r.URL.Query().Get("arrow_id"),
		Limit:   queryInt(r, "limit", 100),
		Offset:  queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListRequirements(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"requirements": rows})
}

// handleClassifications: GET /v2/classifications?arrow_id=...&limit=...
func (s *Server) handleClassifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	filter := engine.RequirementFilter{
		ArrowID: r.URL.Query().Get("arrow_id"),
		Limit:   queryInt(r, "limit", 100),
		Offset:  queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListClassifications(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"classifications": rows})
}

// handleEvaluationRuns: GET /v2/evaluation-runs?clause_id=...&pass_id=...&arrow_id=...&limit=...
func (s *Server) handleEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.engine == nil {
		http.Error(w, "engine not attached", http.StatusServiceUnavailable)
		return
	}
	filter := engine.RunFilter{
		ClauseID: r.URL.Query().Get("clause_id"),
		PassID:   r.URL.Query().Get("pass_id"),
		ArrowID:  r.URL.Query().Get("arrow_id"),
		Limit:    queryInt(r, "limit", 100),
		Offset:   queryInt(r, "offset", 0),
	}
	rows, err := s.engine.ListEvaluationRuns(r.Context(), filter)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, map[string]any{"evaluation_runs": rows})
}
