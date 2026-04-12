package memory

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrKeyPermissions = errors.New("memory: private key has insecure file permissions")

// DeviceKey holds a loaded ed25519 key pair.
type DeviceKey struct {
	DeviceID   string
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

// LoadOrGenerateKey loads an existing key pair or generates a new one.
// Invariant 29: key pair exists before first checkpoint.
func LoadOrGenerateKey(keysDir string, deviceID string) (*DeviceKey, error) {
	privPath := filepath.Join(keysDir, deviceID+".key")
	pubPath := filepath.Join(keysDir, deviceID+".pub")

	// Check if key exists
	if _, err := os.Stat(privPath); err == nil {
		return loadKey(privPath, pubPath, deviceID)
	}

	// Generate new key pair
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return nil, fmt.Errorf("memory: create keys dir: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("memory: generate key: %w", err)
	}

	// Write private key (PEM, mode 0600)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: priv.Seed(),
	})
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return nil, fmt.Errorf("memory: write private key: %w", err)
	}

	// Write public key (PEM)
	pubBytes, err := MarshalPublicKey(pub)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(pubPath, pubBytes, 0644); err != nil {
		return nil, fmt.Errorf("memory: write public key: %w", err)
	}

	return &DeviceKey{DeviceID: deviceID, PrivateKey: priv, PublicKey: pub}, nil
}

func loadKey(privPath, pubPath, deviceID string) (*DeviceKey, error) {
	// Check permissions on private key
	info, err := os.Stat(privPath)
	if err != nil {
		return nil, fmt.Errorf("memory: stat private key: %w", err)
	}
	if info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("%w (got %o, expected 0600)", ErrKeyPermissions, info.Mode().Perm())
	}

	// Read private key
	privPEM, err := os.ReadFile(privPath)
	if err != nil {
		return nil, fmt.Errorf("memory: read private key: %w", err)
	}
	block, _ := pem.Decode(privPEM)
	if block == nil {
		return nil, fmt.Errorf("memory: invalid PEM in private key")
	}
	priv := ed25519.NewKeyFromSeed(block.Bytes)
	pub := priv.Public().(ed25519.PublicKey)

	return &DeviceKey{DeviceID: deviceID, PrivateKey: priv, PublicKey: pub}, nil
}

// MarshalPublicKey encodes an ed25519 public key as PEM.
func MarshalPublicKey(pub ed25519.PublicKey) ([]byte, error) {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "ED25519 PUBLIC KEY",
		Bytes: pub,
	}), nil
}

// UnmarshalPublicKey decodes an ed25519 public key from PEM.
func UnmarshalPublicKey(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("memory: invalid PEM in public key")
	}
	return ed25519.PublicKey(block.Bytes), nil
}
