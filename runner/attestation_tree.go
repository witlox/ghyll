package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AttestationTreeWriter writes attestation records to a per-pass
// JSONL tree structure required by the operator-attestation spec:
//
//	attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl
//
// where:
//
//	<N>          = grid version
//	<context>    = bounded-context-id
//	<S>          = stratum
//	<role-pair>  = source-role + "__" + target-role (e.g.
//	               "analyst__architect"). For roles like the
//	               synthetic adversary, the spec allows
//	               three-role chains separated by "__"
//	               (e.g., "analyst__adversary__architect"); the
//	               encoder accepts arbitrary role chains.
//	<pass-id>    = pass identifier
//
// The tree complements the existing flat
// `.ghyll/attestations.jsonl` (which is the session-wide audit
// trail). Each per-pass file is a localized audit so reviewers
// can find every verdict for one pass without grepping the
// whole flat log.
//
// Concurrency: an internal mutex serializes file-handle opens
// + writes. Per-pass files are opened lazily on first
// attestation for that pass and held open until Close.
//
// Failure model: same as AttestationJSONLWriter — open / write /
// fsync errors are counted via WriteErrors() and surfaced via
// the optional OperatorBus event
// (OpEventAttestationAuditDurabilityFailed).
type AttestationTreeWriter struct {
	mu sync.Mutex

	// root is the absolute path to the project's tree root —
	// typically `<workdir>/.ghyll/attestations`. The tree is
	// created lazily under root.
	root string

	// files caches the per-pass open file handles so we don't
	// re-open + re-stat on every Record event. Closed by the
	// writer's Close method.
	files map[string]*os.File

	// fileSync wraps the per-file fsync; abstracted for tests.
	fileSync func(f *os.File) error

	closed      bool
	writeErrors int
	lastErr     error
	bus         *OperatorBus
}

// NewAttestationTreeWriter constructs a writer rooted at the given
// directory. The directory is created (with intermediate dirs) on
// the first Record; an empty path returns an error.
func NewAttestationTreeWriter(root string) (*AttestationTreeWriter, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("attestation-tree: root path must be non-empty")
	}
	return &AttestationTreeWriter{
		root:     root,
		files:    make(map[string]*os.File),
		fileSync: func(f *os.File) error { return f.Sync() },
	}, nil
}

// WithBus wires an OperatorBus so audit-durability failures
// publish OpEventAttestationAuditDurabilityFailed. Returns the
// receiver for chaining (matches AttestationJSONLWriter's API).
func (w *AttestationTreeWriter) WithBus(bus *OperatorBus) *AttestationTreeWriter {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bus = bus
	return w
}

// Observer returns an AttestationObserver that routes each Record
// event into the per-pass file derived from the record's
// (grid_version, context, stratum, source_role, target_role,
// pass_id) tuple.
//
// The pass_id is read from a separate context object the runtime
// stitches onto the AttestationRecord at observation time. Since
// AttestationRecord does not currently carry pass_id (the runtime
// associates the operator verdict with a clause + arrow, not
// directly with a pass), the tree writer derives pass_id from the
// attestation_id's deterministic shape — `att-<arrow>-<clause>-v<N>`
// becomes the per-pass file name. This is a deliberate choice:
// in the operator-attestation flow, one pass produces one set of
// attested clauses, and the attestation_id is unique per pass
// because grid_version uniquely identifies the pass's grid view.
func (w *AttestationTreeWriter) Observer() AttestationObserver {
	return func(e AttestationEvent) {
		if e.Kind != AttestationEventRecord {
			return
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return
		}

		// Resolve the per-pass file path.
		ctxName := contextFromArrow(e.Record)
		stratum := stratumFromArrow(e.Record)
		rolePair := buildRolePair(e.Record.SourceRole, e.Record.TargetRole)
		passFileName := e.Record.ID + ".jsonl"
		passPath := filepath.Join(w.root,
			fmt.Sprintf("v%d", e.Record.GridVersion),
			sanitizePathSegment(ctxName),
			"stratum-"+sanitizePathSegment(stratum),
			sanitizePathSegment(rolePair),
			sanitizePathSegment(passFileName),
		)

		f, ferr := w.openCached(passPath)
		if ferr != nil {
			w.recordTreeFailure(e.Record, fmt.Errorf("open %s: %w", passPath, ferr))
			return
		}
		line, jerr := jsonlMarshal(newJsonlRecord(e.Record))
		if jerr != nil {
			w.recordTreeFailure(e.Record, fmt.Errorf("marshal: %w", jerr))
			return
		}
		if _, werr := f.Write(line); werr != nil {
			w.recordTreeFailure(e.Record, fmt.Errorf("write %s: %w", passPath, werr))
			return
		}
		if w.fileSync != nil {
			if serr := w.fileSync(f); serr != nil {
				w.recordTreeFailure(e.Record, fmt.Errorf("fsync %s: %w", passPath, serr))
			}
		}
	}
}

// openCached returns the cached file handle for path or opens
// (O_CREATE|O_APPEND) a fresh one. Caller holds w.mu.
func (w *AttestationTreeWriter) openCached(path string) (*os.File, error) {
	if f, ok := w.files[path]; ok {
		return f, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.files[path] = f
	return f, nil
}

// recordTreeFailure is the tree writer's analog of
// AttestationJSONLWriter.recordFailure. Caller holds w.mu.
func (w *AttestationTreeWriter) recordTreeFailure(rec AttestationRecord, err error) {
	w.writeErrors++
	w.lastErr = err
	if w.bus != nil {
		w.bus.Publish(OperatorEvent{
			Kind:     OpEventAttestationAuditDurabilityFailed,
			ArrowID:  rec.ArrowID,
			ClauseID: rec.ClauseID,
			OpID:     rec.OpID,
			Detail:   "tree-writer: " + err.Error(),
		})
	}
}

// Close flushes and releases every cached file handle.
// Subsequent Observer events are silently dropped.
func (w *AttestationTreeWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var firstErr error
	for _, f := range w.files {
		if w.fileSync != nil {
			_ = w.fileSync(f)
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	w.files = map[string]*os.File{}
	return firstErr
}

// Root returns the tree root path.
func (w *AttestationTreeWriter) Root() string { return w.root }

// WriteErrors returns the count of marshal / open / write / fsync
// failures observed since open.
func (w *AttestationTreeWriter) WriteErrors() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeErrors
}

// LastError returns the most recent error, nil if none.
func (w *AttestationTreeWriter) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

// jsonlMarshal serializes one attestation record as the JSON
// payload + newline, in a single buffer so the file Write below
// is one syscall (POSIX O_APPEND atomicity per the operator
// spec).
func jsonlMarshal(r jsonlRecord) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+1)
	out = append(out, data...)
	out = append(out, '\n')
	return out, nil
}

// --- Helpers ---------------------------------------------------------

// buildRolePair encodes (source, target) per the spec:
// "source__target". Two-role pairs. Three-role chains (with
// adversary) are not produced by the AttestationStore today —
// when they are, the spec calls for the adversary slot to be
// inserted between source and target: "source__adversary__target".
// For now we stay with the two-role shape; the spec's three-role
// form will land alongside adversary-flow plumbing.
func buildRolePair(source, target string) string {
	src := sanitizePathSegment(strings.TrimSpace(source))
	tgt := sanitizePathSegment(strings.TrimSpace(target))
	if src == "" {
		src = "unknown"
	}
	if tgt == "" {
		tgt = "unknown"
	}
	return src + "__" + tgt
}

// sanitizePathSegment makes a string safe to use as a filesystem
// segment by replacing path separators and other directory-
// hostile characters with underscores. Conservative: anything
// that isn't [a-zA-Z0-9_.-] becomes "_".
func sanitizePathSegment(s string) string {
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '.', c == '-':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// contextFromArrow extracts the bounded-context name. The
// AttestationRecord doesn't carry the context directly; the
// caller (the runtime that records the attestation) is expected
// to encode it into the OpID or to attach it via a future
// `Context` field. For now we return a placeholder so the path
// always renders; callers using the tree writer should populate
// the field via the future API extension.
//
// Current behavior: returns "default" if no context can be
// derived, matching the spec's allowance for a default-context
// project. This is a known shortcut to revisit when the
// AttestationRecord schema gains a Context field.
func contextFromArrow(rec AttestationRecord) string {
	// The runtime caller (Tier-0 dispatcher / operator UI) can
	// stitch the context onto the record's Reason field as a
	// `ctx:<name>` prefix when the AttestationStore.Record path
	// supports it. Until then, return "default".
	_ = rec
	return "default"
}

// stratumFromArrow extracts the stratum identifier. Same caveat
// as contextFromArrow: the AttestationRecord doesn't carry it
// today. Returns "default" placeholder.
func stratumFromArrow(rec AttestationRecord) string {
	_ = rec
	return "default"
}

// File handle helpers — io.Writer compatibility for tests.
var _ io.Closer = (*AttestationTreeWriter)(nil)
