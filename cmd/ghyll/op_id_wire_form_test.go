package main

import (
	"errors"
	"strings"
	"testing"
)

// TestScenario_ValidateOpID_ReturnsTypedSentinels verifies the
// gate-2 CORR-A-12 sentinel-error wiring — the BDD scenario at
// attestation.feature:175-186 requires "op-id-required",
// "op-id-too-long", "op-id-invalid-characters" wire forms.
func TestScenario_ValidateOpID_ReturnsTypedSentinels(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrOpIDRequired},
		{"too long", strings.Repeat("a", maxOpIDBytes+1), ErrOpIDTooLong},
		{"control byte", "alice\x00bob", ErrOpIDInvalidCharacters},
		{"path separator", "alice/bob", ErrOpIDInvalidCharacters},
		{"backslash", "alice\\bob", ErrOpIDInvalidCharacters},
		{"whitespace", "alice bob", ErrOpIDInvalidCharacters},
		{"dot dot", "..alice", ErrOpIDInvalidCharacters},
		{"leading dot", ".alice", ErrOpIDInvalidCharacters},
		{"leading dash", "-alice", ErrOpIDInvalidCharacters},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOpID(tc.in)
			if !errors.Is(err, tc.want) {
				t.Errorf("validateOpID(%q) = %v; want errors.Is %v", tc.in, err, tc.want)
			}
		})
	}
}

// TestScenario_ValidateOpID_AcceptsValidIDs verifies the happy
// paths still pass with the sentinel wiring in place.
func TestScenario_ValidateOpID_AcceptsValidIDs(t *testing.T) {
	for _, id := range []string{
		"alice",
		"alice@example.com",
		"team:platform",
		"u123",
		"ops_team_42",
	} {
		if err := validateOpID(id); err != nil {
			t.Errorf("validateOpID(%q) = %v; want nil", id, err)
		}
	}
}
