package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScenario_VaultClient_Search maps to:
// Scenario: Search team memory via vault
func TestScenario_VaultClient_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/search" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"checkpoint": map[string]any{
						"v": 1, "hash": "abc", "summary": "test result",
						"parent": "000", "device": "dev1", "author": "alice",
						"ts": 1, "session": "s1", "turn": 1, "model": "m25",
						"sig": "xyz",
					},
					"similarity": 0.85,
				},
			},
		})
	}))
	defer server.Close()

	client := NewVaultClient(server.URL, "test-token")
	results, err := client.Search([]float32{0.1, 0.2}, "repo-hash", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Similarity != 0.85 {
		t.Errorf("similarity = %f", results[0].Similarity)
	}
}

// TestScenario_VaultClient_BearerToken maps to:
// Scenario: Vault search with bearer token
func TestScenario_VaultClient_BearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer team-secret" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	client := NewVaultClient(server.URL, "team-secret")
	_, err := client.Search([]float32{0.1}, "repo", 5)
	if err != nil {
		t.Fatalf("expected success with correct token: %v", err)
	}
}

// TestScenario_VaultClient_LocalhostNoAuth maps to:
// Scenario: Vault on localhost without token
func TestScenario_VaultClient_LocalhostNoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("expected no auth header for localhost, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	// The test server URL is 127.0.0.1 which should be detected as localhost
	client := NewVaultClient(server.URL, "")
	_, err := client.Search([]float32{0.1}, "repo", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestScenario_VaultClient_Unreachable maps to:
// Scenario: Vault unreachable
func TestScenario_VaultClient_Unreachable(t *testing.T) {
	client := NewVaultClient("http://localhost:1", "token")
	_, err := client.Search([]float32{0.1}, "repo", 5)
	if err == nil {
		t.Fatal("expected error for unreachable vault")
	}
}

// TestScenario_VaultClient_Push maps to:
// Scenario: Push checkpoint to vault
func TestScenario_VaultClient_Push(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/checkpoints" {
			received = true
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	client := NewVaultClient(server.URL, "token")
	err := client.Push(&Checkpoint{
		Hash: "abc", Summary: "test", Signature: "sig",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !received {
		t.Error("server did not receive push")
	}
}
