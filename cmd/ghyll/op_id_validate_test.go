package main

import (
	"strings"
	"testing"
)

func TestScenario_ValidateOpID_AcceptsTypicalIdentities(t *testing.T) {
	cases := []string{
		"alice",
		"alice@example.com",
		"u123",
		"team:platform",
		"ops_team_42",
	}
	for _, c := range cases {
		if err := validateOpID(c); err != nil {
			t.Errorf("validateOpID(%q) = %v; want nil", c, err)
		}
	}
}

func TestScenario_ValidateOpID_RejectsControlBytes(t *testing.T) {
	cases := []string{
		"alice\x00bob",
		"alice\nbob",
		"alice\rbob",
		"alice\x1Bbob",
		"alice\x7Fbob",
	}
	for _, c := range cases {
		err := validateOpID(c)
		if err == nil {
			t.Errorf("validateOpID(%q) returned nil; want error", c)
		}
	}
}

func TestScenario_ValidateOpID_RejectsPathSeparators(t *testing.T) {
	cases := []string{
		"alice/bob",
		"alice\\bob",
		"../alice",
		"path/to/alice",
	}
	for _, c := range cases {
		err := validateOpID(c)
		if err == nil {
			t.Errorf("validateOpID(%q) returned nil; want error", c)
		}
	}
}

func TestScenario_ValidateOpID_RejectsDotDot(t *testing.T) {
	if err := validateOpID("alice..bob"); err == nil {
		t.Error("validateOpID accepted '..' substring")
	}
}

func TestScenario_ValidateOpID_RejectsLeadingDotOrDash(t *testing.T) {
	for _, c := range []string{".alice", "-flag"} {
		if err := validateOpID(c); err == nil {
			t.Errorf("validateOpID(%q) accepted leading punct", c)
		}
	}
}

func TestScenario_ValidateOpID_RejectsEmptyAndOverlong(t *testing.T) {
	if err := validateOpID(""); err == nil {
		t.Error("validateOpID accepted empty")
	}
	overlong := strings.Repeat("a", maxOpIDBytes+1)
	if err := validateOpID(overlong); err == nil {
		t.Error("validateOpID accepted overlong identity")
	}
}

func TestScenario_OperatorCmd_OpID_RejectsControlByte(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/op-id alice\x00bob")
	if !strings.Contains(r.Output, "control byte") {
		t.Errorf("expected control-byte rejection; got %q", r.Output)
	}
}

func TestScenario_OperatorCmd_OpID_RejectsPathTraversal(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/op-id ..alice")
	if !strings.Contains(r.Output, "..") {
		t.Errorf("expected path-traversal rejection; got %q", r.Output)
	}
}

func TestScenario_OperatorCmd_OpID_RejectsSlash(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/op-id alice/bob")
	if !strings.Contains(r.Output, "path-separator") {
		t.Errorf("expected path-separator rejection; got %q", r.Output)
	}
}
