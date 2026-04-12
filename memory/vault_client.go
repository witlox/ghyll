package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

var ErrVaultUnavailable = errors.New("memory: vault server unreachable")

// VaultClient handles HTTP communication with ghyll-vault.
// Timeout: 5s per request (FM-12).
type VaultClient struct {
	url        string
	token      string
	isLocal    bool
	httpClient *http.Client
}

// NewVaultClient creates a vault client. Detects localhost for auth bypass (invariant 26).
func NewVaultClient(vaultURL string, token string) *VaultClient {
	return &VaultClient{
		url:     vaultURL,
		token:   token,
		isLocal: isLocalhost(vaultURL),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Search finds checkpoints by embedding similarity.
func (c *VaultClient) Search(embedding []float32, repoHash string, topK int) ([]SearchResult, error) {
	body := map[string]any{
		"embedding": embedding,
		"repo":      repoHash,
		"top_k":     topK,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("memory: vault marshal: %w", err)
	}

	req, err := http.NewRequest("POST", c.url+"/v1/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("memory: vault request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("memory: vault search HTTP %d", resp.StatusCode)
	}

	var result struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("memory: vault decode: %w", err)
	}
	return result.Results, nil
}

// Push sends a checkpoint to the vault.
// Push failures are logged, not fatal (invariant 13 spirit).
func (c *VaultClient) Push(cp *Checkpoint) error {
	body := map[string]any{
		"checkpoint": cp,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memory: vault marshal: %w", err)
	}

	req, err := http.NewRequest("POST", c.url+"/v1/checkpoints", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("memory: vault request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		return fmt.Errorf("memory: vault push HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *VaultClient) setAuth(req *http.Request) {
	if c.token == "" {
		return // Invariant 26: no token → no auth (covers localhost)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
}

func isLocalhost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
