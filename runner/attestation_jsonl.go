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
// Per ADR-010: durability lives in the engine sqlite table. The
// JSONL file is a derived audit trail; if it is deleted, the
// engine can re-export it (`ghyll engine export-attestations`).
//
// The writer is thread-safe: an internal mutex serializes appends
// so concurrent Record events don't interleave bytes.
type AttestationJSONLWriter struct {
	mu      sync.Mutex
	path    string
	out     io.WriteCloser
	closed  bool
	writeFn func(io.Writer, []byte) error
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
			// Marshal of a well-formed record can't fail in
			// practice (all fields are strings/ints), but guard
			// anyway.
			return
		}
		_ = w.writeFn(w.out, line)
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

func writeLine(w io.Writer, line []byte) error {
	if _, err := w.Write(line); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}
