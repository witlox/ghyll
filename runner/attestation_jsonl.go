package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// AttestationJSONLWriter appends one JSON line per attestation
// record to a project-local audit file (typically
// `.ghyll/attestations.jsonl`). Subscribed to the
// AttestationStore's Observer so it captures every Record at the
// moment it fires — the file and the in-memory store stay
// consistent.
//
// Per ADR-010 + the operator-attestation spec: durability lives
// in the engine sqlite table; the JSONL file is a derived audit
// trail. Each line is fsync'd BEFORE the Record call returns,
// satisfying the spec invariant "the file is fsync'd before the
// verdict is reported as accepted." If the fsync fails, the
// record is counted as a write error AND the on-disk line may be
// half-flushed — operators should consult LastError() and
// reconcile against the engine table.
//
// The writer is thread-safe: an internal mutex serializes appends
// so concurrent Record events don't interleave bytes. Each line
// is written in a single Write call (json + newline pre-joined),
// so a process crash mid-syscall cannot leave a partial line
// without leaving NO line — `O_APPEND` on the underlying file
// guarantees position-atomicity for writes up to PIPE_BUF size,
// and a single JSONL record stays well under that limit.
//
// Write failures are tracked via WriteErrors() — the Observer
// callback cannot return errors (per the AttestationObserver
// contract), so failures are counted instead. Callers should
// inspect WriteErrors() at session end to detect audit-trail
// loss.
type AttestationJSONLWriter struct {
	mu          sync.Mutex
	path        string
	out         io.WriteCloser
	closed      bool
	writeFn     func(io.Writer, []byte) error
	syncFn      func() error // fsync the underlying file; nil = no-op
	writeErrors int
	lastErr     error

	// Bus (optional) receives OpEventAttestationAuditDurability-
	// Failed events when a marshal / write / fsync error occurs.
	// Wired via WithBus. Surfacing failures via the bus gives
	// operator-facing UIs and the engine status CLI a real-time
	// signal that the audit trail diverged from the engine table,
	// without forcing the AttestationStore.Record contract to
	// fail (the engine table is the durable source of truth per
	// ADR-010; the JSONL is a derived audit trail).
	bus *OperatorBus
}

// WithBus wires an OperatorBus so the writer publishes a typed
// event on any audit-durability failure. Returns the receiver for
// chaining.
func (w *AttestationJSONLWriter) WithBus(bus *OperatorBus) *AttestationJSONLWriter {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bus = bus
	return w
}

// NewAttestationJSONLWriter opens (or creates+appends to) path.
// Returns an error if the parent directory does not exist OR the
// file cannot be opened. The caller MUST call Close() to release
// the file handle.
func NewAttestationJSONLWriter(path string) (*AttestationJSONLWriter, error) {
	if path == "" {
		return nil, errors.New("attestation-jsonl: path must be non-empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("attestation-jsonl: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("attestation-jsonl: open %s: %w", path, err)
	}
	return &AttestationJSONLWriter{
		path:    path,
		out:     f,
		writeFn: writeLine,
		syncFn:  f.Sync, // fsync-before-accept (operator spec invariant)
	}, nil
}

// newAttestationJSONLWriterForWriter is a test-only constructor
// that bypasses the filesystem. Used by unit tests with
// bytes.Buffer.
func newAttestationJSONLWriterForWriter(w io.WriteCloser) *AttestationJSONLWriter {
	return &AttestationJSONLWriter{out: w, writeFn: writeLine}
}

// Observer returns an AttestationObserver that appends one JSON
// line per Record event. Wire via
// `attestationStore.Observe(writer.Observer())`. Observer events
// AFTER Close are silently dropped (callers should Close at
// session end after Flush).
//
// Marshal and write failures increment the internal error counter;
// inspect via WriteErrors() and LastError() at session end. The
// Observer cannot return errors per the AttestationObserver
// contract — surfacing on the bus or via slog is the alternative.
func (w *AttestationJSONLWriter) Observer() AttestationObserver {
	return func(e AttestationEvent) {
		if e.Kind != AttestationEventRecord {
			return
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return
		}
		line, err := json.Marshal(newJsonlRecord(e.Record))
		if err != nil {
			w.recordFailure(e.Record, fmt.Errorf("marshal attestation %s: %w", e.Record.ID, err))
			return
		}
		if err := w.writeFn(w.out, line); err != nil {
			w.recordFailure(e.Record, fmt.Errorf("write attestation %s: %w", e.Record.ID, err))
			return
		}
		// fsync-before-accept (operator spec invariant). The
		// Observer returns AFTER fsync so the AttestationStore.Record
		// caller only proceeds once the audit row is durable on
		// disk. If fsync fails, we surface the failure via the
		// bus AND the WriteErrors counter so the operator path
		// sees it — but the Observer returns normally because the
		// AttestationObserver contract has no error channel and
		// the engine table is the durable source of truth (the
		// JSONL is a derived audit trail per ADR-010).
		if w.syncFn != nil {
			if err := w.syncFn(); err != nil {
				w.recordFailure(e.Record, fmt.Errorf("fsync attestation %s: %w", e.Record.ID, err))
			}
		}
	}
}

// recordFailure is called under w.mu. Increments the error
// counter, captures the last error, and publishes a typed bus
// event so operator-facing surfaces see the failure in real time.
func (w *AttestationJSONLWriter) recordFailure(rec AttestationRecord, err error) {
	w.writeErrors++
	w.lastErr = err
	if w.bus != nil {
		w.bus.Publish(OperatorEvent{
			Kind:     OpEventAttestationAuditDurabilityFailed,
			ArrowID:  rec.ArrowID,
			ClauseID: rec.ClauseID,
			OpID:     rec.OpID,
			Detail:   err.Error(),
		})
	}
}

// WriteErrors returns the count of marshal or write failures
// observed since open. A non-zero count signals audit-trail loss;
// the operator should inspect LastError() and reconcile against
// the engine table (`ghyll engine export-attestations`).
func (w *AttestationJSONLWriter) WriteErrors() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeErrors
}

// LastError returns the most recent marshal or write error, nil if
// none.
func (w *AttestationJSONLWriter) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

// TruncateAt seeks the underlying file to offset and truncates
// everything past it. Called by session.Open once after a Recovery
// signal that LoadFromJSONL detected a truncated trailing line
// (F-6). The next Record overwrites the partial bytes; new
// records append cleanly.
//
// No-op when the underlying writer is not an *os.File (the test-
// only constructor uses a bytes.Buffer with no truncation surface).
func (w *AttestationJSONLWriter) TruncateAt(offset int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("attestation-jsonl: writer closed")
	}
	f, ok := w.out.(*os.File)
	if !ok {
		return nil
	}
	if err := f.Truncate(offset); err != nil {
		return fmt.Errorf("attestation-jsonl: truncate %s @%d: %w", w.path, offset, err)
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return fmt.Errorf("attestation-jsonl: seek %s @%d: %w", w.path, offset, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("attestation-jsonl: sync after truncate %s: %w", w.path, err)
	}
	return nil
}

// PrimaryWriter returns a function suitable for
// AttestationStore.SetPrimaryWriter. The returned closure
// performs the same marshal + write + fsync as the Observer but
// returns the error inline so the store can refuse the Record
// call (ADR-015 Part C inversion). The Observer path is left
// in place as a fallback for code that doesn't use the primary-
// writer wiring.
func (w *AttestationJSONLWriter) PrimaryWriter() func(AttestationRecord) error {
	return func(rec AttestationRecord) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return errors.New("attestation-jsonl: writer closed")
		}
		line, err := json.Marshal(newJsonlRecord(rec))
		if err != nil {
			w.recordFailure(rec, fmt.Errorf("marshal: %w", err))
			return fmt.Errorf("marshal: %w", err)
		}
		if err := w.writeFn(w.out, line); err != nil {
			w.recordFailure(rec, fmt.Errorf("write: %w", err))
			return fmt.Errorf("write: %w", err)
		}
		if w.syncFn != nil {
			if err := w.syncFn(); err != nil {
				w.recordFailure(rec, fmt.Errorf("fsync: %w", err))
				return fmt.Errorf("fsync: %w", err)
			}
		}
		return nil
	}
}

// Close flushes (sync the file) and closes the underlying writer.
// Subsequent Observer events are silently dropped.
func (w *AttestationJSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if f, ok := w.out.(*os.File); ok {
		_ = f.Sync()
	}
	return w.out.Close()
}

// Path returns the file path (empty for the test-only constructor).
func (w *AttestationJSONLWriter) Path() string { return w.path }

// jsonlRecord is the wire shape of one JSONL line. Field names are
// snake_case to match the engine table and the operator-facing
// audit consumer.
type jsonlRecord struct {
	ID             string `json:"attestation_id"`
	Kind           string `json:"kind"`
	ArrowID        string `json:"arrow_id"`
	ClauseID       string `json:"clause_id,omitempty"`
	OpID           string `json:"op_id"`
	AttestedByRole string `json:"attested_by_role"`
	SourceRole     string `json:"source_role,omitempty"`
	TargetRole     string `json:"target_role,omitempty"`
	Verdict        string `json:"verdict"`
	Reason         string `json:"reason,omitempty"`
	Timestamp      int64  `json:"timestamp"`
	GridVersion    uint64 `json:"grid_version"`
}

// newJsonlRecord builds the wire shape from an AttestationRecord.
func newJsonlRecord(rec AttestationRecord) jsonlRecord {
	return jsonlRecord{
		ID:             rec.ID,
		Kind:           string(rec.Kind),
		ArrowID:        rec.ArrowID,
		ClauseID:       rec.ClauseID,
		OpID:           rec.OpID,
		AttestedByRole: rec.AttestedByRole,
		SourceRole:     rec.SourceRole,
		TargetRole:     rec.TargetRole,
		Verdict:        string(rec.Verdict),
		Reason:         rec.Reason,
		Timestamp:      rec.Timestamp,
		GridVersion:    rec.GridVersion,
	}
}

// writeLine emits the JSON payload + newline as a SINGLE Write
// call. Two-call variants (json then "\n") can interleave under
// concurrent appenders if the underlying writer doesn't honor
// O_APPEND atomicity on each call; one-call guarantees that
// O_APPEND moves to end-of-file once, writes the full record,
// and returns. The buffer is recreated per call (no shared state).
func writeLine(w io.Writer, line []byte) error {
	payload := make([]byte, 0, len(line)+1)
	payload = append(payload, line...)
	payload = append(payload, '\n')
	_, err := w.Write(payload)
	return err
}
