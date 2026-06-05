package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestScenario_Engine_DoesNotContainAPIKey is a regression
// assertion: even with a sentinel api_key configured in the
// process environment, the engine's sqlite tables (passes,
// findings, arrows, ...) NEVER contain the secret. The engine
// schema has no column for api_key or endpoint auth; this test
// guards against a future field-addition that would silently
// leak credentials into the durable store.
//
// ADV-AUTH-003 remediation: t.Setenv replaces raw os.Setenv to
// prevent process-env bleed into sibling parallel tests.
//
// Note: this is a unit-level defence-in-depth guard. The full
// end-to-end test (real Session.Turn → engine.db on disk) lives in
// cmd/ghyll/auth_integration_test.go where the wiring is reachable.
func TestScenario_Engine_DoesNotContainAPIKey(t *testing.T) {
	sentinel := "sk-canary-cccc-must-not-leak"
	t.Setenv("GHYLL_API_KEY", sentinel)
	t.Setenv("GHYLL_API_KEY_M25", sentinel)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engine.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	if err := s.UpsertPass(ctx, PassRecord{
		PassID:      "P-canary",
		Role:        "analyst",
		Context:     "default",
		ArrowID:     "A-canary",
		GridVersion: 1,
		State:       "open",
		OpenedAt:    "2026-05-20T10:00:00Z",
	}); err != nil {
		t.Fatalf("UpsertPass: %v", err)
	}
	if err := s.UpsertFinding(ctx, FindingRecord{
		ID:           "F-canary",
		ArrowID:      "A-canary",
		Type:         "local-bug",
		Severity:     1,
		Status:       "open",
		Description:  "ok",
		RaisedAt:     "2026-05-20T10:00:00Z",
		RaisedByRole: "integrator",
		StoreVersion: 1,
		UpdatedAt:    "2026-05-20T10:00:00Z",
	}); err != nil {
		t.Fatalf("UpsertFinding: %v", err)
	}
	// Force a flush before reading the file.
	_ = s.Close()

	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Contains(raw, []byte(sentinel)) {
		t.Fatalf("engine.db leaked sentinel api_key %q", sentinel)
	}
}
