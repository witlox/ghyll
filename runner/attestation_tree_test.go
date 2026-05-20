package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tier2TreeRec returns a fully-populated Tier 2 attestation
// record fixture. Tests adjust individual fields per scenario.
func tier2TreeRec() AttestationRecord {
	return AttestationRecord{
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
		// Tier 2 additions (ADR-016 + gate-1):
		PassID:  "P-1",
		Context: "checkout",
		Stratum: "L1",
	}
}

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
	store.SetPrimaryWriter(w.PrimaryWriter())

	rec := tier2TreeRec()
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}

	// Expected path (Tier 2):
	//   <root>/v3/checkout/stratum-L1/analyst__architect/P-1.jsonl
	expected := filepath.Join(root, "v3", "checkout", "stratum-L1",
		"analyst__architect", "P-1.jsonl")
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

func TestScenario_EncodeAttestationPath_TwoRole(t *testing.T) {
	rec := tier2TreeRec()
	path, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if truncated {
		t.Errorf("clean fixture should not truncate")
	}
	want := filepath.Join("v3", "checkout", "stratum-L1",
		"analyst__architect", "P-1.jsonl")
	if path != want {
		t.Errorf("path = %q; want %q", path, want)
	}
}

func TestScenario_EncodeAttestationPath_ThreeRoleChain(t *testing.T) {
	rec := tier2TreeRec()
	rec.AdversaryRole = "adversary"
	path, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if truncated {
		t.Errorf("clean fixture should not truncate")
	}
	want := filepath.Join("v3", "checkout", "stratum-L1",
		"analyst__adversary__architect", "P-1.jsonl")
	if path != want {
		t.Errorf("path = %q; want %q", path, want)
	}
}

func TestScenario_EncodeAttestationPath_Init(t *testing.T) {
	rec := tier2TreeRec()
	rec.AttestedByRole = "init"
	rec.Context = "anything" // overridden
	rec.Stratum = "anything" // overridden
	rec.SourceRole = "ignored"
	rec.TargetRole = "analyst" // target role appears in role-pair
	path, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if truncated {
		t.Errorf("init fixture should not truncate")
	}
	// Per attestation.feature "init arrow path encoding":
	// role-pair = "init__analyst", context + stratum = "_".
	want := filepath.Join("v3", "_", "stratum-_", "init__analyst", "P-1.jsonl")
	if path != want {
		t.Errorf("path = %q; want %q", path, want)
	}
}

func TestScenario_EncodeAttestationPath_EmptyPassIDRejected(t *testing.T) {
	rec := tier2TreeRec()
	rec.PassID = ""
	_, _, err := EncodeAttestationPath(rec)
	if !errors.Is(err, ErrAttestationPassIDEmpty) {
		t.Errorf("empty PassID: got %v; want ErrAttestationPassIDEmpty", err)
	}
}

func TestScenario_EncodeAttestationPath_ByteCapOverflow(t *testing.T) {
	rec := tier2TreeRec()
	rec.SourceRole = strings.Repeat("a", 300)
	_, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !truncated {
		t.Errorf("oversized role should have triggered truncation")
	}
}

func TestScenario_EncodeAttestationPath_EmptySegmentHashed(t *testing.T) {
	rec := tier2TreeRec()
	rec.Context = "" // empty after sanitize → safeSegment hashes
	path, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !truncated {
		t.Errorf("empty Context should have triggered truncation")
	}
	if !strings.Contains(path, "/h-") {
		t.Errorf("path should contain hash-substituted segment: %q", path)
	}
}

func TestScenario_AttestationTree_SeparatePassFilesPerPassID(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	rec1 := tier2TreeRec()
	rec1.PassID = "P-1"
	rec1.ID = "att-A1-C1-v3"

	rec2 := tier2TreeRec()
	rec2.PassID = "P-2"
	rec2.ID = "att-A1-C1-v4"
	rec2.GridVersion = 4

	_ = store.Record(rec1)
	_ = store.Record(rec2)

	p1 := filepath.Join(root, "v3", "checkout", "stratum-L1",
		"analyst__architect", "P-1.jsonl")
	p2 := filepath.Join(root, "v4", "checkout", "stratum-L1",
		"analyst__architect", "P-2.jsonl")
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected per-pass file %s: %v", p, err)
		}
	}
}

func TestScenario_AttestationTree_SamePassMultipleClausesOneFile(t *testing.T) {
	// One pass produces multiple verdicts (one per clause). All go
	// to the SAME tree file because filename is keyed on PassID.
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	for _, clause := range []string{"C1", "C2", "C3"} {
		rec := tier2TreeRec()
		rec.ID = "att-A1-" + clause + "-v3"
		rec.ClauseID = clause
		if err := store.Record(rec); err != nil {
			t.Fatalf("record %s: %v", clause, err)
		}
	}

	path := filepath.Join(root, "v3", "checkout", "stratum-L1",
		"analyst__architect", "P-1.jsonl")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Count(strings.TrimRight(string(contents), "\n"), "\n") + 1
	if lines != 3 {
		t.Errorf("lines = %d; want 3 (one per clause on the same pass)", lines)
	}
}

func TestScenario_AttestationTree_CloseFlushes(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	rec := tier2TreeRec()
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestScenario_AttestationTree_FsyncFailureFailsRecord(t *testing.T) {
	// Tier 2: tree writer is the PrimaryWriter. fsync failure
	// returns error inline → Record returns
	// ErrAttestationAuditWriteFailed → in-memory store unchanged.
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	w.fileSync = func(_ *os.File) error { return errors.New("disk full") }
	bus := NewOperatorBus()
	w.WithBus(bus)
	defer w.Close()

	var got []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { got = append(got, e) })

	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	rec := tier2TreeRec()
	err := store.Record(rec)
	if !errors.Is(err, ErrAttestationAuditWriteFailed) {
		t.Errorf("Record: got %v; want ErrAttestationAuditWriteFailed", err)
	}
	if store.Len() != 0 {
		t.Errorf("in-memory mutated despite primaryWriter failure: %d entries", store.Len())
	}
	if w.WriteErrors() != 1 {
		t.Errorf("WriteErrors = %d; want 1", w.WriteErrors())
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

func TestScenario_AttestationTree_PathSafeOnHostileSeparators(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	// Role names containing path separators must NOT escape the
	// root. sanitizePathSegment turns them into underscores.
	rec := tier2TreeRec()
	rec.SourceRole = "../escape"
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}
	// Verify the resulting path stays inside root (no traversal).
	walkOK := false
	_ = filepath.Walk(root, func(path string, _ os.FileInfo, _ error) error {
		if strings.HasSuffix(path, "P-1.jsonl") {
			rel, err := filepath.Rel(root, path)
			if err == nil && !strings.HasPrefix(rel, "..") {
				walkOK = true
			}
		}
		return nil
	})
	if !walkOK {
		t.Fatal("path traversal possible — hostile separator escaped sanitization")
	}
}

func TestScenario_AttestationTree_TruncateTrailingPartialAll(t *testing.T) {
	root := t.TempDir()
	w, _ := NewAttestationTreeWriter(root)
	defer w.Close()
	store := NewAttestationStore()
	store.SetPrimaryWriter(w.PrimaryWriter())

	// Write one complete record.
	rec := tier2TreeRec()
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}

	// Manually append a partial trailing line to the per-pass
	// file (simulate a crash mid-write).
	passPath := filepath.Join(root, "v3", "checkout", "stratum-L1",
		"analyst__architect", "P-1.jsonl")
	f, err := os.OpenFile(passPath, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"partial":"not terminated`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	preBytes, _ := os.ReadFile(passPath)

	if err := w.TruncateTrailingPartialAll(root); err != nil {
		t.Fatalf("TruncateTrailingPartialAll: %v", err)
	}

	postBytes, _ := os.ReadFile(passPath)
	if len(postBytes) >= len(preBytes) {
		t.Errorf("file size did not shrink: pre=%d post=%d", len(preBytes), len(postBytes))
	}
	if !strings.HasSuffix(string(postBytes), "\n") {
		t.Errorf("file should end with newline after truncate: %q", postBytes)
	}
	if strings.Contains(string(postBytes), "partial") {
		t.Errorf("partial line still in file: %q", postBytes)
	}
}
