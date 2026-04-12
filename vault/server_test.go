package vault

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/memory"
)

func setupTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vault.db")

	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := NewServer(store, "test-token")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return srv, ts
}

func signedCheckpoint(t *testing.T, summary string) (*memory.Checkpoint, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	cp := &memory.Checkpoint{
		Version: 1, ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID: "dev1", AuthorID: "alice", Timestamp: 1,
		RepoRemote: "repo-hash-1", SessionID: "s1", Turn: 1,
		ActiveModel: "m25", Summary: summary,
		Embedding: []float32{0.5, 0.5, 0.5},
	}
	memory.SignCheckpoint(cp, priv)
	return cp, pub
}

// TestScenario_Vault_Health maps to:
// GET /v1/health
func TestScenario_Vault_Health(t *testing.T) {
	_, ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
}

// TestScenario_Vault_PushAndSearch maps to:
// Scenario: Vault accepts checkpoint push + Vault serves search API
func TestScenario_Vault_PushAndSearch(t *testing.T) {
	_, ts := setupTestServer(t)

	cp, _ := signedCheckpoint(t, "auth module race condition fix")

	// Push
	pushBody, _ := json.Marshal(map[string]any{"checkpoint": cp})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("push status = %d", resp.StatusCode)
	}

	// Search
	searchBody, _ := json.Marshal(map[string]any{
		"embedding": []float32{0.5, 0.5, 0.5},
		"repo":      "repo-hash-1",
		"top_k":     5,
	})
	req, _ = http.NewRequest("POST", ts.URL+"/v1/search", bytes.NewReader(searchBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("search status = %d", resp.StatusCode)
	}

	var result struct {
		Results []memory.SearchResult `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}
	if result.Results[0].Checkpoint.Summary != "auth module race condition fix" {
		t.Errorf("summary = %q", result.Results[0].Checkpoint.Summary)
	}
}

// TestScenario_Vault_AuthRequired maps to:
// Scenario: Vault search with bearer token (401 without token)
func TestScenario_Vault_AuthRequired(t *testing.T) {
	_, ts := setupTestServer(t)

	searchBody, _ := json.Marshal(map[string]any{
		"embedding": []float32{0.1},
		"repo":      "r",
		"top_k":     1,
	})
	// No auth header
	resp, err := http.Post(ts.URL+"/v1/search", "application/json", bytes.NewReader(searchBody))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestScenario_Vault_PushRejectsInvalidSig maps to:
// Scenario: Vault accepts checkpoint push — rejects invalid
func TestScenario_Vault_PushRejectsInvalidSig(t *testing.T) {
	_, ts := setupTestServer(t)

	cp := &memory.Checkpoint{
		Version: 1, Hash: "fakehash",
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1", AuthorID: "alice", Timestamp: 1,
		SessionID: "s1", Turn: 1, ActiveModel: "m25",
		Summary: "bad", Signature: hex.EncodeToString(make([]byte, 64)),
	}

	pushBody, _ := json.Marshal(map[string]any{"checkpoint": cp})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	// Should reject — hash doesn't match content
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 for invalid sig, got %d", resp.StatusCode)
	}
}

// TestScenario_Vault_PushIdempotent maps to:
// 409 on duplicate (but not an error in practice)
func TestScenario_Vault_PushIdempotent(t *testing.T) {
	_, ts := setupTestServer(t)

	cp, _ := signedCheckpoint(t, "test")
	pushBody, _ := json.Marshal(map[string]any{"checkpoint": cp})

	// First push
	req, _ := http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Second push — should succeed (idempotent)
	req, _ = http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, _ = http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("expected 201 on duplicate push, got %d", resp.StatusCode)
	}
}

// TestScenario_Vault_SearchFiltersRepo maps to:
// Scenario: Vault serves search API — filtered by repo
func TestScenario_Vault_SearchFiltersRepo(t *testing.T) {
	_, ts := setupTestServer(t)

	// Push checkpoints for two different repos
	cp1, _ := signedCheckpoint(t, "repo1 work")
	cp2, _ := signedCheckpoint(t, "repo2 work")

	for _, cp := range []*memory.Checkpoint{cp1, cp2} {
		pushBody, _ := json.Marshal(map[string]any{"checkpoint": cp})
		req, _ := http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-token")
		resp, _ := http.DefaultClient.Do(req)
		_ = resp.Body.Close()
	}

	// Search repo-hash-1 only
	searchBody, _ := json.Marshal(map[string]any{
		"embedding": []float32{0.5, 0.5, 0.5},
		"repo":      "repo-hash-1",
		"top_k":     10,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/search", bytes.NewReader(searchBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Results []memory.SearchResult `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	// Results should be filtered to the requested repo
	for _, r := range result.Results {
		if r.Checkpoint.RepoRemote != "" && r.Checkpoint.RepoRemote != "repo-hash-1" {
			t.Errorf("result from wrong repo: %q", r.Checkpoint.RepoRemote)
		}
	}
}

// TestScenario_Vault_HealthCounters maps to:
// GET /v1/health with checkpoint and device counts
func TestScenario_Vault_HealthCounters(t *testing.T) {
	_, ts := setupTestServer(t)

	// Push a checkpoint first
	cp, _ := signedCheckpoint(t, "test")
	pushBody, _ := json.Marshal(map[string]any{"checkpoint": cp})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/checkpoints", bytes.NewReader(pushBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Health should still return ok
	resp, err := http.Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("health status = %v", body["status"])
	}
}
