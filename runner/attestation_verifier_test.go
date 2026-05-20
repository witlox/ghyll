package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleJSONLLine returns one valid attestation row matching the
// shape AttestationJSONLWriter would emit. Tests mutate one field
// per case to exercise validation.
func sampleJSONLLine() string {
	return `{"attestation_id":"att-A1-C1-v1","kind":"depth-type","arrow_id":"A1","clause_id":"C1","op_id":"alice","attested_by_role":"operator","source_role":"analyst","target_role":"architect","verdict":"pass","reason":"verified","timestamp":1747663200000000000,"grid_version":1}`
}

func TestScenario_AttestationVerifier_HappyPath(t *testing.T) {
	v := &AttestationVerifier{}
	res, err := v.Verify(strings.NewReader(sampleJSONLLine() + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || res.OK != 1 || res.Lines != 1 {
		t.Fatalf("got %+v; want OK=1 Failed=0 Lines=1", res)
	}
}

func TestScenario_AttestationVerifier_SkipsBlankAndCommentLines(t *testing.T) {
	v := &AttestationVerifier{}
	input := "\n# comment\n  \n" + sampleJSONLLine() + "\n# another comment\n"
	res, err := v.Verify(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if res.Lines != 1 || res.OK != 1 {
		t.Fatalf("blank/comment lines should be skipped; got %+v", res)
	}
}

func TestScenario_AttestationVerifier_MalformedJSON(t *testing.T) {
	v := &AttestationVerifier{}
	res, _ := v.Verify(strings.NewReader("not json at all\n"))
	if res.Failed != 1 {
		t.Fatalf("expected Failed=1; got %+v", res)
	}
	if !strings.Contains(res.Issues[0].Reason, "json-unmarshal") {
		t.Fatalf("issue should mention json-unmarshal; got %q", res.Issues[0].Reason)
	}
}

func TestScenario_AttestationVerifier_DetectsMissingFields(t *testing.T) {
	v := &AttestationVerifier{}
	cases := []struct {
		name    string
		mutate  func(s string) string
		mention string
	}{
		{"empty id", func(s string) string { return strings.Replace(s, `"att-A1-C1-v1"`, `""`, 1) }, "attestation_id is empty"},
		{"empty arrow", func(s string) string { return strings.Replace(s, `"A1"`, `""`, 1) }, "arrow_id is empty"},
		{"empty op-id", func(s string) string { return strings.Replace(s, `"alice"`, `""`, 1) }, "op_id is empty"},
		{"zero timestamp", func(s string) string {
			return strings.Replace(s, `"timestamp":1747663200000000000`, `"timestamp":0`, 1)
		}, "timestamp must be > 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := c.mutate(sampleJSONLLine())
			res, _ := v.Verify(strings.NewReader(line + "\n"))
			found := false
			for _, i := range res.Issues {
				if strings.Contains(i.Reason, c.mention) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected issue mentioning %q; got %+v", c.mention, res.Issues)
			}
		})
	}
}

func TestScenario_AttestationVerifier_KindClauseIDPairing(t *testing.T) {
	v := &AttestationVerifier{}
	// depth-type with empty clause_id.
	bad := strings.Replace(sampleJSONLLine(), `"clause_id":"C1"`, `"clause_id":""`, 1)
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 {
		t.Fatalf("expected 1 failure; got %+v", res)
	}
	// on-the-spot with non-empty clause_id.
	bad2 := strings.Replace(sampleJSONLLine(), `"kind":"depth-type"`, `"kind":"on-the-spot"`, 1)
	res, _ = v.Verify(strings.NewReader(bad2 + "\n"))
	if res.Failed != 1 {
		t.Fatalf("on-the-spot+clause should fail; got %+v", res)
	}
}

func TestScenario_AttestationVerifier_UnknownKindAndVerdict(t *testing.T) {
	v := &AttestationVerifier{}
	bad := strings.Replace(sampleJSONLLine(), `"kind":"depth-type"`, `"kind":"bogus"`, 1)
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 {
		t.Fatalf("unknown kind should fail; got %+v", res)
	}
	bad2 := strings.Replace(sampleJSONLLine(), `"verdict":"pass"`, `"verdict":"maybe"`, 1)
	res, _ = v.Verify(strings.NewReader(bad2 + "\n"))
	if res.Failed != 1 {
		t.Fatalf("unknown verdict should fail; got %+v", res)
	}
}

func TestScenario_AttestationVerifier_SelfCert(t *testing.T) {
	v := &AttestationVerifier{}
	// AttestedByRole = SourceRole ("analyst")
	bad := strings.Replace(sampleJSONLLine(), `"attested_by_role":"operator"`, `"attested_by_role":"analyst"`, 1)
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 {
		t.Fatalf("source-role self-cert should fail; got %+v", res)
	}
	if !strings.Contains(res.Issues[0].Reason, "source_role") {
		t.Fatalf("issue should mention source_role; got %q", res.Issues[0].Reason)
	}
	// AttestedByRole = TargetRole ("architect")
	bad2 := strings.Replace(sampleJSONLLine(), `"attested_by_role":"operator"`, `"attested_by_role":"architect"`, 1)
	res, _ = v.Verify(strings.NewReader(bad2 + "\n"))
	if res.Failed != 1 {
		t.Fatalf("target-role self-cert should fail; got %+v", res)
	}
}

func TestScenario_AttestationVerifier_AggregatesMultipleLinesAndReportsAll(t *testing.T) {
	v := &AttestationVerifier{}
	good := sampleJSONLLine()
	bad := strings.Replace(good, `"verdict":"pass"`, `"verdict":"maybe"`, 1)
	res, err := v.Verify(strings.NewReader(good + "\n" + bad + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK != 1 || res.Failed != 1 || res.Lines != 2 {
		t.Fatalf("got %+v; want OK=1 Failed=1 Lines=2", res)
	}
	if res.Issues[0].Line != 2 {
		t.Errorf("issue line = %d; want 2", res.Issues[0].Line)
	}
}

func TestScenario_AttestationVerifier_VerifyFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "att.jsonl")
	if err := os.WriteFile(path, []byte(sampleJSONLLine()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &AttestationVerifier{}
	res, err := v.VerifyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || res.OK != 1 {
		t.Fatalf("got %+v; want OK=1", res)
	}
}

func TestScenario_AttestationVerifier_VerifyFile_MissingPath(t *testing.T) {
	v := &AttestationVerifier{}
	_, err := v.VerifyFile("/nonexistent/path/here.jsonl")
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}

// Roundtrip: write via the production JSONL writer, verify via
// the production verifier. This pins the wire-format contract
// between writer and verifier.
func TestScenario_AttestationVerifier_RoundtripWithProductionWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "att.jsonl")
	w, err := NewAttestationJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewAttestationStore()
	store.Observe(w.Observer())
	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Reason: "verified",
		Timestamp: 1747663200_000000000, GridVersion: 1, PassID: "P-roundtrip",
	}
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	v := &AttestationVerifier{}
	res, err := v.VerifyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 {
		t.Fatalf("verifier rejected production-writer output: %s", res.String())
	}
}

func TestScenario_AttestationVerifier_StringRendering(t *testing.T) {
	v := &AttestationVerifier{}
	good := sampleJSONLLine()
	res, _ := v.Verify(strings.NewReader(good + "\n"))
	if !strings.Contains(res.String(), "1/1 records OK") {
		t.Fatalf("clean summary: %s", res.String())
	}
	bad := strings.Replace(good, `"verdict":"pass"`, `"verdict":"maybe"`, 1)
	res, _ = v.Verify(strings.NewReader(bad + "\n"))
	if !strings.Contains(res.String(), "failed") {
		t.Fatalf("dirty summary: %s", res.String())
	}
}
