package memory

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// VerificationResult reports the outcome of hash chain + signature verification.
type VerificationResult struct {
	Valid    bool
	DeviceID string
	FailedAt string // checkpoint hash where verification failed
	Reason   string // "broken_chain", "bad_signature", "hash_mismatch", "unknown_key"
}

// CanonicalHash computes sha256 over the canonical JSON of a checkpoint.
// Invariant 4: deterministic — sorted keys, no whitespace, excludes hash and sig.
func CanonicalHash(cp *Checkpoint) string {
	// Build a map of all fields except hash and sig
	m := map[string]any{
		"v":       cp.Version,
		"parent":  cp.ParentHash,
		"device":  cp.DeviceID,
		"author":  cp.AuthorID,
		"ts":      cp.Timestamp,
		"repo":    cp.RepoRemote,
		"branch":  cp.Branch,
		"session": cp.SessionID,
		"turn":    cp.Turn,
		"model":   cp.ActiveModel,
		"summary": cp.Summary,
		"files":   cp.FilesTouched,
		"tools":   cp.ToolsUsed,
	}

	// v2 fields
	if cp.PlanMode {
		m["plan_mode"] = cp.PlanMode
	}
	if cp.ResumedFrom != nil {
		m["resumed_from"] = cp.ResumedFrom
	}

	// Embedding excluded from hash — it's for search, not integrity.
	// Float serialization varies across platforms, which would break verification.
	if len(cp.InjectionSig) > 0 {
		m["injections"] = cp.InjectionSig
	}

	// Sort keys and marshal
	data := canonicalJSON(m)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// canonicalJSON produces JSON with sorted keys and no extra whitespace.
func canonicalJSON(m map[string]any) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build sorted JSON manually to guarantee key order
	buf := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		keyBytes, _ := json.Marshal(k)
		valBytes, _ := json.Marshal(m[k])
		buf = append(buf, keyBytes...)
		buf = append(buf, ':')
		buf = append(buf, valBytes...)
	}
	buf = append(buf, '}')
	return buf
}

// SignCheckpoint computes the hash and signs it.
func SignCheckpoint(cp *Checkpoint, priv ed25519.PrivateKey) {
	cp.Hash = CanonicalHash(cp)
	sig := ed25519.Sign(priv, []byte(cp.Hash))
	cp.Signature = hex.EncodeToString(sig)
}

// VerifyCheckpoint verifies hash integrity and signature.
func VerifyCheckpoint(cp *Checkpoint, pub ed25519.PublicKey) VerificationResult {
	// Recompute hash
	computed := CanonicalHash(cp)
	if computed != cp.Hash {
		return VerificationResult{
			Valid:    false,
			DeviceID: cp.DeviceID,
			FailedAt: cp.Hash,
			Reason:   "hash_mismatch",
		}
	}

	// Verify signature
	sig, err := hex.DecodeString(cp.Signature)
	if err != nil {
		return VerificationResult{
			Valid:    false,
			DeviceID: cp.DeviceID,
			FailedAt: cp.Hash,
			Reason:   "bad_signature",
		}
	}

	if !ed25519.Verify(pub, []byte(cp.Hash), sig) {
		return VerificationResult{
			Valid:    false,
			DeviceID: cp.DeviceID,
			FailedAt: cp.Hash,
			Reason:   "bad_signature",
		}
	}

	return VerificationResult{Valid: true, DeviceID: cp.DeviceID}
}

// VerifyChain verifies hash chain integrity for an ordered list of checkpoints.
// Invariant 1: each checkpoint's ParentHash must match the previous checkpoint's Hash.
func VerifyChain(checkpoints []Checkpoint) VerificationResult {
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	for i, cp := range checkpoints {
		if i == 0 {
			if cp.ParentHash != zeroHash {
				return VerificationResult{
					Valid:    false,
					DeviceID: cp.DeviceID,
					FailedAt: cp.Hash,
					Reason:   "broken_chain",
				}
			}
			continue
		}

		if cp.ParentHash != checkpoints[i-1].Hash {
			return VerificationResult{
				Valid:    false,
				DeviceID: cp.DeviceID,
				FailedAt: cp.Hash,
				Reason:   "broken_chain",
			}
		}
	}

	return VerificationResult{Valid: true}
}
