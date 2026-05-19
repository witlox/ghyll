package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenario_AttestationTree_EmptyRootRejected(t *testing.T) {
	_, err := NewAttestationTreeWriter("")
	if err == nil {
		t.Fatal("empty root must error")
	}
}

func TestScenario_AttestationTree_WritesPerPassFile(t *testing.T) {
	root := t.TempDir()
	w, err := NewAttestationTreeWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID:             "att-A1-C1-v3",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    3,
	}
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}

	// Expected path:
	//   <root>/v3/default/stratum-default/analyst__architect/att-A1-C1-v3.jsonl
	expected := filepath.Join(root, "v3", "default", "stratum-default",
		"analyst__architect", "att-A1-C1-v3.jsonl")
	info, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("expected per-pass file at %s: %v", expected, err)
	}
	if info.Size() == 0 {
		t.Fatal("per-pass file is empty")
	}
	contents, err := os.ReadFile(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(contents), "\n") {
		t.Fatalf("missing trailing newline; got %q", contents)
	}
	if !strings.Contains(string(contents), `"attestation_id":"att-A1-C1-v3"`) {
		t.Fatalf("missing id in JSONL; got %q", contents)
	}
}

func TestScenario_AttestationTree_RolePairEncoded(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID: "att-A2-v1", Kind: AttestationKindOnTheSpot,
		ArrowID: "A2", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "implementer", TargetRole: "integrator",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)

	expected := filepath.Join(root, "v1", "default", "stratum-default",
		"implementer__integrator", "att-A2-v1.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("role-pair path %s: %v", expected, err)
	}
}

func TestScenario_AttestationTree_SeparatePassFilesPerRecord(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.Observe(w.Observer())

	// Two records with different IDs and grid versions land in
	// separate per-pass files.
	rec1 := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	rec2 := AttestationRecord{
		ID: "att-A1-C1-v2", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationFail, Timestamp: 2, GridVersion: 2,
	}
	_ = store.Record(rec1)
	_ = store.Record(rec2)

	p1 := filepath.Join(root, "v1", "default", "stratum-default",
		"analyst__architect", "att-A1-C1-v1.jsonl")
	p2 := filepath.Join(root, "v2", "default", "stratum-default",
		"analyst__architect", "att-A1-C1-v2.jsonl")
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected per-pass file %s: %v", p, err)
		}
	}
}

func TestScenario_AttestationTree_CloseFlushes(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Subsequent record events silently drop.
	rec.ID = "att-A1-C2-v1"
	rec.ClauseID = "C2"
	_ = store.Record(rec)
	p2 := filepath.Join(root, "v1", "default", "stratum-default",
		"analyst__architect", "att-A1-C2-v1.jsonl")
	if _, err := os.Stat(p2); err == nil {
		t.Fatal("post-Close event should not write")
	}
}

func TestScenario_AttestationTree_FsyncFailurePublishesEvent(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	w.fileSync = func(_ *os.File) error { return errors.New("disk full") }
	bus := NewOperatorBus()
	w.WithBus(bus)
	defer w.Close()

	var got []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { got = append(got, e) })

	store := NewAttestationStore()
	store.Observe(w.Observer())
	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)

	if w.WriteErrors() != 1 {
		t.Fatalf("WriteErrors = %d; want 1", w.WriteErrors())
	}
	var sawEvent bool
	for _, e := range got {
		if e.Kind == OpEventAttestationAuditDurabilityFailed {
			sawEvent = true
			if !strings.Contains(e.Detail, "tree-writer") {
				t.Errorf("event detail should mark tree-writer: %q", e.Detail)
			}
		}
	}
	if !sawEvent {
		t.Fatal("expected audit-durability event")
	}
}

func TestScenario_AttestationTree_SanitizesPathSegments(t *testing.T) {
	cases := []struct{ in, want string }{
		{"analyst", "analyst"},
		{"two words", "two_words"},
		{"with/slash", "with_slash"},
		{"v1", "v1"},
		{"a.b-c_d", "a.b-c_d"},
	}
	for _, c := range cases {
		if got := sanitizePathSegment(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestScenario_AttestationTree_EmptyRoleFillsUnknown(t *testing.T) {
	if got := buildRolePair("", "architect"); got != "unknown__architect" {
		t.Errorf("empty source: got %q", got)
	}
	if got := buildRolePair("analyst", ""); got != "analyst__unknown" {
		t.Errorf("empty target: got %q", got)
	}
}

func TestScenario_AttestationTree_PathSafeOnHostileSeparators(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.Observe(w.Observer())

	// Role names containing path separators must NOT escape the
	// root. sanitizePathSegment turns them into underscores.
	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator",
		SourceRole:     "../escape",
		TargetRole:     "architect",
		Verdict:        AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	_ = store.Record(rec)
	// Path must stay inside root.
	expected := filepath.Join(root, "v1", "default", "stratum-default",
		".._escape__architect", "att-A1-C1-v1.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("sanitized path %s: %v", expected, err)
	}
}
