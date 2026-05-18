package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/engine"
)

// helper: spin up a vault server with an engine attached + seed data.
func newTestServerWithEngine(t *testing.T) (*httptest.Server, *engine.Store) {
	t.Helper()
	store, err := engine.OpenStore(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Seed a finding + an arrow + a run for the read endpoints.
	ctx := context.Background()
	_ = store.UpsertFinding(ctx, engine.FindingRecord{
		ID: "F1", ArrowID: "A1", Type: "local-bug",
		Severity: 3, Status: "open", Description: "test",
	})
	_ = store.InsertGridArrow(ctx, engine.GridArrowRecord{
		ID: "A1", GridVersion: 1,
		SourceRole: "analyst", TargetRole: "architect",
		Stratum: "L4", Context: "checkout",
		ClausesJSON: "[]", RequirementsJSON: "[]", Kind: "append",
	})
	_ = store.UpsertRequirement(ctx, engine.RequirementRecord{
		ArrowID: "A1", ReqID: "R1", MinDepth: 2, Description: "checkout",
	})
	_ = store.UpsertClassification(ctx, engine.ClassificationRecord{
		ArrowID: "A1", ReqID: "R1", Observed: 3, Evidence: "live pg",
	})
	_ = store.UpsertAmendment(ctx, engine.AmendmentRecord{
		ID: "am1", Reason: "missing-cross-context-spec",
		SourceArrow: "A1", TargetRole: "analyst",
		ContextsJSON: "[]", FindingIDsJSON: "[]",
		CreatedAt: "t0",
	})
	_ = store.InsertEvaluationRun(ctx, engine.EvaluationRunRecord{
		ID: "run-1", ClauseID: "C1", PassID: "P1",
		ArrowID: "A1", EvaluatorConcept: "lint", EvaluatorGeneration: 1,
		StartedAt: "t0", CompletedAt: "t1",
		EndStatus: "pass",
	})

	// Pass nil memory.Store — the v2 endpoints don't need it.
	s := NewServer(nil, "")
	s.AttachEngine(store)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = store.Close()
	})
	return ts, store
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestVault_V2_FindingsList(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/findings?min_severity=-1")
	findings, _ := body["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("findings = %d; want 1", len(findings))
	}
}

func TestVault_V2_FindingsFilter(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/findings?arrow_id=A1&min_severity=3")
	findings, _ := body["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("filtered findings = %d; want 1", len(findings))
	}
}

func TestVault_V2_FindingsFilterNoMatch(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/findings?arrow_id=nope")
	findings, _ := body["findings"].([]any)
	if len(findings) != 0 {
		t.Errorf("no-match returned %d findings; want 0", len(findings))
	}
}

func TestVault_V2_Arrows(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/arrows")
	arrows, _ := body["arrows"].([]any)
	if len(arrows) != 1 {
		t.Errorf("arrows = %d; want 1", len(arrows))
	}
}

func TestVault_V2_Requirements(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/requirements?arrow_id=A1")
	reqs, _ := body["requirements"].([]any)
	if len(reqs) != 1 {
		t.Errorf("requirements = %d; want 1", len(reqs))
	}
}

func TestVault_V2_Classifications(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/classifications?arrow_id=A1")
	cls, _ := body["classifications"].([]any)
	if len(cls) != 1 {
		t.Errorf("classifications = %d; want 1", len(cls))
	}
}

func TestVault_V2_Amendments(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/amendments?drained=false")
	ams, _ := body["amendments"].([]any)
	if len(ams) != 1 {
		t.Errorf("amendments = %d; want 1", len(ams))
	}
}

func TestVault_V2_EvaluationRuns(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	body := getJSON(t, ts.URL+"/v2/evaluation-runs?arrow_id=A1")
	runs, _ := body["evaluation_runs"].([]any)
	if len(runs) != 1 {
		t.Errorf("runs = %d; want 1", len(runs))
	}
}

func TestVault_V2_AuthRequiredWhenTokenSet(t *testing.T) {
	store, _ := engine.OpenStore(filepath.Join(t.TempDir(), "engine.db"))
	defer func() { _ = store.Close() }()
	s := NewServer(nil, "secret-token")
	s.AttachEngine(store)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// No auth → 401.
	resp, _ := http.Get(ts.URL + "/v2/findings")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", resp.StatusCode)
	}

	// With auth → 200.
	req, _ := http.NewRequest("GET", ts.URL+"/v2/findings", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp2, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("authed status = %d; want 200", resp2.StatusCode)
	}
}

func TestVault_V2_TransitionsRequiresFindingID(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	resp, err := http.Get(ts.URL + "/v2/findings/transitions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing finding_id: status = %d; want 400", resp.StatusCode)
	}
}

func TestVault_V2_EngineNotAttached(t *testing.T) {
	s := NewServer(nil, "")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/v2/findings")
	defer func() { _ = resp.Body.Close() }()
	// Without AttachEngine, the v2 endpoint isn't registered on the
	// mux at all, so we get 404 (Go's default).
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("no-attach status = %d; want 404", resp.StatusCode)
	}
}

func TestVault_V2_MethodNotAllowed(t *testing.T) {
	ts, _ := newTestServerWithEngine(t)
	resp, _ := http.Post(ts.URL+"/v2/findings", "application/json", strings.NewReader("{}"))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d; want 405", resp.StatusCode)
	}
}
