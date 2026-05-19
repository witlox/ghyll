package vault

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/witlox/ghyll/engine"
)

// v2 endpoints serve structured-query reads over the v2 entities.
// Hardenings (validation-pass-9):
//   - V1: AmendmentRecord.MarshalJSON renders DrainedAt as string|null.
//   - V2: AttachEngine guards against duplicate registration.
//   - V3: writeServerError logs detail server-side; client gets a
//     generic body.
//   - V4: queryInt returns 400 on non-empty unparseable values.
//   - V5: queryBool case-insensitive on the known tokens; 400 on
//     anything else non-empty.
//   - V6: uint64 fields serialize as JSON strings via record tags.
//   - V10: case-insensitive bearer scheme + constant-time compare.
//   - V11: query-param values capped at maxQueryValueLen.
//   - V14: writeJSON logs encode errors.
//   - V15: handleTransitions exposes offset.

// maxQueryValueLen bounds any single query-param value. 256 chars
// is enough for any legitimate ID + slack for URL encoding.
const maxQueryValueLen = 256

// AttachEngine wires the v2 endpoints onto the existing mux. Per
// V2: panics if called more than once on the same Server (route
// re-registration would panic anyway; this surfaces the misuse with
// a clear message).
func (s *Server) AttachEngine(store *engine.Store) {
	if s.engineAttached {
		panic("vault: AttachEngine called twice; engine wiring must be a one-shot")
	}
	if store == nil {
		panic("vault: AttachEngine called with nil store")
	}
	s.engine = store
	s.engineAttached = true
	s.mux.HandleFunc("/v2/findings", s.authMiddleware(s.handleFindings))
	s.mux.HandleFunc("/v2/findings/transitions", s.authMiddleware(s.handleTransitions))
	s.mux.HandleFunc("/v2/amendments", s.authMiddleware(s.handleAmendments))
	s.mux.HandleFunc("/v2/arrows", s.authMiddleware(s.handleArrows))
	s.mux.HandleFunc("/v2/requirements", s.authMiddleware(s.handleRequirements))
	s.mux.HandleFunc("/v2/classifications", s.authMiddleware(s.handleClassifications))
	s.mux.HandleFunc("/v2/evaluation-runs", s.authMiddleware(s.handleEvaluationRuns))
}

// queryString returns the param if it's non-empty and within
// length bounds; otherwise sets a 400 error on w and returns ("",
// false). Empty (absent) is fine — returns ("", true).
func queryString(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	raw := r.URL.Query().Get(key)
	if len(raw) > maxQueryValueLen {
		http.Error(w, "query value too long: "+key, http.StatusBadRequest)
		return "", false
	}
	return raw, true
}

// queryInt parses an int from the query string. Returns the int
// and true on success; on parse failure of a non-empty value,
// writes 400 to w and returns (0, false). Empty returns (def, true).
func queryInt(w http.ResponseWriter, r *http.Request, key string, def int) (int, bool) {
	raw, ok := queryString(w, r, key)
	if !ok {
		return 0, false
	}
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		http.Error(w, "query value not an int: "+key, http.StatusBadRequest)
		return 0, false
	}
	return n, true
}

// queryBool parses an optional bool. Returns (nil, true) for absent.
// Returns (&value, true) for any case-insensitive form of true/false/1/0.
// On any other non-empty value, writes 400 to w and returns (nil, false).
func queryBool(w http.ResponseWriter, r *http.Request, key string) (*bool, bool) {
	raw, ok := queryString(w, r, key)
	if !ok {
		return nil, false
	}
	if raw == "" {
		return nil, true
	}
	low := strings.ToLower(raw)
	switch low {
	case "true", "1":
		t := true
		return &t, true
	case "false", "0":
		f := false
		return &f, true
	}
	http.Error(w, "query value not a bool: "+key, http.StatusBadRequest)
	return nil, false
}

// writeJSON serializes v as JSON. Encode errors log but cannot
// change the HTTP status at that point (headers already sent).
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// V14: don't silently lose this.
		if s.logger != nil {
			s.logger.Printf("vault.v2: encode %s %s: %v", r.Method, r.URL.Path, err)
		}
	}
}

// writeServerError logs the detail server-side and returns a
// generic body to the client (V3). Internal errors must not leak
// schema names or file paths via verbose sqlite messages.
func (s *Server) writeServerError(r *http.Request, w http.ResponseWriter, err error) {
	if s.logger != nil {
		s.logger.Printf("vault.v2: %s %s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// handleFindings: GET /v2/findings?arrow_id=...&status=...&min_severity=...&type=...&limit=...&offset=...
func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	arrowID, ok := queryString(w, r, "arrow_id")
	if !ok {
		return
	}
	status, ok := queryString(w, r, "status")
	if !ok {
		return
	}
	typ, ok := queryString(w, r, "type")
	if !ok {
		return
	}
	minSev, ok := queryInt(w, r, "min_severity", -1)
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListFindings(r.Context(), engine.FindingFilter{
		ArrowID: arrowID, Status: status, MinSeverity: minSev, Type: typ,
		Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	total, err := s.engine.CountFindings(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("findings", rows, len(rows), total, limit, offset))
}

// handleTransitions: GET /v2/findings/transitions?finding_id=...&limit=...&offset=...
func (s *Server) handleTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	findingID, ok := queryString(w, r, "finding_id")
	if !ok {
		return
	}
	if findingID == "" {
		http.Error(w, "finding_id required", http.StatusBadRequest)
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListTransitions(r.Context(), findingID, limit, offset)
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	// Transitions don't have a CountX yet; estimate has_more from
	// page-full + offset; total is omitted.
	s.writeJSON(w, r, partialPaginated("transitions", rows, len(rows), limit, offset))
}

// handleAmendments: GET /v2/amendments?source_arrow=...&drained=true|false&limit=...&offset=...
func (s *Server) handleAmendments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sourceArrow, ok := queryString(w, r, "source_arrow")
	if !ok {
		return
	}
	drained, ok := queryBool(w, r, "drained")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListAmendments(r.Context(), engine.AmendmentFilter{
		SourceArrow: sourceArrow, Drained: drained, Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	pending, drainedCount, err := s.engine.CountAmendments(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("amendments", rows, len(rows), pending+drainedCount, limit, offset))
}

// handleArrows: GET /v2/arrows?kind=append|on-the-spot&min_grid_version=...&limit=...&offset=...
func (s *Server) handleArrows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	kind, ok := queryString(w, r, "kind")
	if !ok {
		return
	}
	minGrid, ok := queryInt(w, r, "min_grid_version", 0)
	if !ok {
		return
	}
	if minGrid < 0 {
		http.Error(w, "min_grid_version must be >= 0", http.StatusBadRequest)
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListArrows(r.Context(), engine.ArrowFilter{
		Kind: kind, MinGridVer: uint64(minGrid), Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	total, err := s.engine.CountArrows(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("arrows", rows, len(rows), total, limit, offset))
}

// handleRequirements: GET /v2/requirements?arrow_id=...&limit=...&offset=...
func (s *Server) handleRequirements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	arrowID, ok := queryString(w, r, "arrow_id")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListRequirements(r.Context(), engine.RequirementFilter{
		ArrowID: arrowID, Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	total, err := s.engine.CountRequirements(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("requirements", rows, len(rows), total, limit, offset))
}

// handleClassifications: GET /v2/classifications?arrow_id=...&limit=...&offset=...
func (s *Server) handleClassifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	arrowID, ok := queryString(w, r, "arrow_id")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListClassifications(r.Context(), engine.ClassificationFilter{
		ArrowID: arrowID, Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	total, err := s.engine.CountClassifications(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("classifications", rows, len(rows), total, limit, offset))
}

// handleEvaluationRuns: GET /v2/evaluation-runs?clause_id=...&pass_id=...&arrow_id=...&limit=...&offset=...
func (s *Server) handleEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clauseID, ok := queryString(w, r, "clause_id")
	if !ok {
		return
	}
	passID, ok := queryString(w, r, "pass_id")
	if !ok {
		return
	}
	arrowID, ok := queryString(w, r, "arrow_id")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit", 100)
	if !ok {
		return
	}
	if limit > 1000 {
		http.Error(w, "limit exceeds maximum (1000)", http.StatusBadRequest)
		return
	}
	offset, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return
	}
	rows, err := s.engine.ListEvaluationRuns(r.Context(), engine.RunFilter{
		ClauseID: clauseID, PassID: passID, ArrowID: arrowID,
		Limit: limit, Offset: offset,
	})
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	total, err := s.engine.CountEvaluationRuns(r.Context())
	if err != nil {
		s.writeServerError(r, w, err)
		return
	}
	s.writeJSON(w, r, paginatedResponse("evaluation_runs", rows, len(rows), total, limit, offset))
}

// paginatedResponse formats a list-endpoint body with pagination
// metadata so clients can detect truncation without re-issuing
// a count query. Per integrator M5.
//
// Layout:
//
//	{ "<key>": [...], "page": { "total": N, "limit": L, "offset": O, "has_more": bool } }
func paginatedResponse(key string, rows any, returned, total, limit, offset int) map[string]any {
	hasMore := offset+returned < total
	return map[string]any{
		key: rows,
		"page": map[string]any{
			"total":    total,
			"limit":    limit,
			"offset":   offset,
			"returned": returned,
			"has_more": hasMore,
		},
	}
}

// partialPaginated is the same shape but without an accurate
// `total`, used for endpoints where a Count helper is not (yet)
// available. `has_more` is conservative: true when the page filled.
func partialPaginated(key string, rows any, returned, limit, offset int) map[string]any {
	return map[string]any{
		key: rows,
		"page": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"returned": returned,
			"has_more": returned >= limit,
		},
	}
}

// silence the errors import — required by go vet on unused-import.
var _ = errors.New
