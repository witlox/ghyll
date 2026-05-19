package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// nopWriteCloser wraps a bytes.Buffer as io.WriteCloser for the
// test constructor.
type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

func TestScenario_AttestationJSONL_AppendsOnRecord(t *testing.T) {
	buf := &bytes.Buffer{}
	w := newAttestationJSONLWriterForWriter(nopWriteCloser{buf})
	defer w.Close()

	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "op-alice",
		AttestedByRole: "implementer",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
	}
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output missing trailing newline: %q", out)
	}
	var got jsonlRecord
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\nraw: %s", err, out)
	}
	if got.ID != rec.ID {
		t.Errorf("ID = %q; want %q", got.ID, rec.ID)
	}
	if got.Kind != string(rec.Kind) {
		t.Errorf("Kind = %q; want %q", got.Kind, rec.Kind)
	}
	if got.Verdict != string(rec.Verdict) {
		t.Errorf("Verdict = %q; want %q", got.Verdict, rec.Verdict)
	}
}

func TestScenario_AttestationJSONL_OnTheSpot_OmitsClauseID(t *testing.T) {
	buf := &bytes.Buffer{}
	w := newAttestationJSONLWriterForWriter(nopWriteCloser{buf})
	defer w.Close()
	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID:             "att-A2-v1",
		Kind:           AttestationKindOnTheSpot,
		ArrowID:        "A2",
		OpID:           "op-bob",
		AttestedByRole: "integrator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
	}
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"clause_id"`) {
		t.Fatalf("on-the-spot JSONL row should omit clause_id; got %s", buf.String())
	}
}

func TestScenario_AttestationJSONL_DoesNotFire_OnIdempotentReRecord(t *testing.T) {
	buf := &bytes.Buffer{}
	w := newAttestationJSONLWriterForWriter(nopWriteCloser{buf})
	defer w.Close()
	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "op-alice",
		AttestedByRole: "implementer", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)
	_ = store.Record(rec) // idempotent — no observer event

	count := strings.Count(buf.String(), "\n")
	if count != 1 {
		t.Fatalf("got %d lines; want 1 (idempotent re-Record must not duplicate audit row)", count)
	}
}

func TestScenario_AttestationJSONL_AfterClose_DropsEvents(t *testing.T) {
	buf := &bytes.Buffer{}
	w := newAttestationJSONLWriterForWriter(nopWriteCloser{buf})
	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "op-alice",
		AttestedByRole: "implementer", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)
	_ = w.Close()

	rec2 := rec
	rec2.ID = "att-A1-C2-v1"
	rec2.ClauseID = "C2"
	_ = store.Record(rec2) // observer should silently drop

	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Fatalf("got %d lines after Close; want 1 (post-Close events dropped)", got)
	}
}

func TestScenario_AttestationJSONL_FileWriter_CreatesParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "attestations.jsonl")
	w, err := NewAttestationJSONLWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if w.Path() != path {
		t.Fatalf("Path = %q; want %q", w.Path(), path)
	}
}

func TestScenario_AttestationJSONL_EmptyPath_Errors(t *testing.T) {
	_, err := NewAttestationJSONLWriter("")
	if err == nil {
		t.Fatal("empty path should error")
	}
}

// ensure NewAttestationJSONLWriter returns a value that compiles
// as an io.Closer (helps catch accidental signature changes).
var _ io.Closer = (*AttestationJSONLWriter)(nil)
