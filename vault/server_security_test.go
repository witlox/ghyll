package vault

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
)

// TestScenario_Vault_Checkpoints_RequiresValidSignature verifies
// Tier 3 / SR C-1: vault refuses an unsigned checkpoint when
// keysDir is configured.
func TestScenario_Vault_Checkpoints_RequiresValidSignature(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := NewServer(store, "").WithKeysDir(keysDir)
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	// Build a forged checkpoint — recompute the hash but use a
	// random signature.
	cp := memory.Checkpoint{
		Version: 1, DeviceID: "alice",
		Summary: "attacker summary", AuthorID: "evil",
	}
	cp.Hash = memory.CanonicalHash(&cp)
	cp.Signature = hex.EncodeToString(bytes.Repeat([]byte{0x00}, 64))

	body, _ := json.Marshal(map[string]any{"checkpoint": cp})
	resp, err := http.Post(httpSrv.URL+"/v1/checkpoints", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d; want 403 forbidden (no device key)", resp.StatusCode)
	}
}

// TestScenario_Vault_Checkpoints_ValidSignatureAccepted verifies
// the happy path: a properly signed checkpoint with a registered
// device key is accepted.
func TestScenario_Vault_Checkpoints_ValidSignatureAccepted(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Generate the device key + drop the pub file into keysDir.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := memory.MarshalPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "alice.pub"), pubBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := NewServer(store, "").WithKeysDir(keysDir)
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	cp := memory.Checkpoint{
		Version: 1, DeviceID: "alice",
		Summary: "real summary", AuthorID: "alice",
	}
	cp.Hash = memory.CanonicalHash(&cp)
	sig := ed25519.Sign(priv, []byte(cp.Hash))
	cp.Signature = hex.EncodeToString(sig)

	body, _ := json.Marshal(map[string]any{"checkpoint": cp})
	resp, err := http.Post(httpSrv.URL+"/v1/checkpoints", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		got, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d; want 201 (body=%s)", resp.StatusCode, string(got))
	}
}

// TestScenario_Vault_Checkpoints_RefusesPathTraversalDeviceID
// covers Tier 3 / SR C-3 prevention at the vault entry point —
// even with a forged sig, the path-traversal device id rejects
// before file open.
func TestScenario_Vault_Checkpoints_RefusesPathTraversalDeviceID(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := NewServer(store, "").WithKeysDir(keysDir)
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	cp := memory.Checkpoint{
		Version: 1, DeviceID: "../../etc/passwd",
		Summary: "x", AuthorID: "a",
	}
	cp.Hash = memory.CanonicalHash(&cp)
	cp.Signature = hex.EncodeToString(bytes.Repeat([]byte{0}, 64))

	body, _ := json.Marshal(map[string]any{"checkpoint": cp})
	resp, err := http.Post(httpSrv.URL+"/v1/checkpoints", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d; want 403 (path-traversal device id)", resp.StatusCode)
	}
}

// TestScenario_Vault_Search_BoundedRequestBody verifies Tier 3 /
// SR C-6: oversized request body is rejected.
func TestScenario_Vault_Search_BoundedRequestBody(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := NewServer(store, "")
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	// Build a 10 MiB body whose ALL bytes must be parsed (single
	// JSON string field). MaxBytesReader fires at 4 MiB before
	// the decoder finishes.
	huge := strings.Repeat("a", 10*1024*1024)
	body := `{"repo":"r","embedding":[],"junk":"` + huge + `"}`
	resp, err := http.Post(httpSrv.URL+"/v1/search", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("oversized body accepted; want failure")
	}
}
