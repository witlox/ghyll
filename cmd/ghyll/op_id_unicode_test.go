package main

import (
	"errors"
	"testing"
)

// TestScenario_ValidateOpID_RejectsRTLOverride verifies gate-2
// CORR-A-21 / SEC-L-1: Unicode format runes (RTL override,
// zero-width space, etc.) are rejected even though their UTF-8
// encoding passes the byte-level checks. Uses \u escapes to
// keep staticcheck ST1018 happy.
func TestScenario_ValidateOpID_RejectsRTLOverride(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"RTL override", "alice\u202e.gpj"},
		{"LTR override", "alice\u202d.gpj"},
		{"zero-width space", "alice\u200bbob"},
		{"zero-width joiner", "alice\u200dbob"},
		{"BOM", "\ufefffoo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOpID(tc.id)
			if !errors.Is(err, ErrOpIDInvalidCharacters) {
				t.Errorf("validateOpID(%q) = %v; want ErrOpIDInvalidCharacters", tc.id, err)
			}
		})
	}
}

// TestScenario_ValidateOpID_RejectsTrailingDot verifies gate-2
// SEC-L-2: trailing '.' is rejected.
func TestScenario_ValidateOpID_RejectsTrailingDot(t *testing.T) {
	err := validateOpID("alice.")
	if !errors.Is(err, ErrOpIDInvalidCharacters) {
		t.Errorf("validateOpID(%q) = %v; want ErrOpIDInvalidCharacters", "alice.", err)
	}
}
