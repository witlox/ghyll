package runner

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// Tier 3 coverage push — small accessors.

func TestScenario_FindingRecord_AsDeriveInput(t *testing.T) {
	rec := FindingRecord{
		ID: "F-1", ArrowID: "A", Severity: SeverityHigh,
		Status: FindingStatusOpen, RaisedByRole: "adversary",
	}
	in := rec.AsDeriveInput()
	if in.SeverityRank != SeverityHigh {
		t.Errorf("SeverityRank = %v; want SeverityHigh", in.SeverityRank)
	}
	if in.Status != FindingStatusOpen {
		t.Errorf("Status = %v; want open", in.Status)
	}
}

func TestScenario_isSymlinkOpenError_Classification(t *testing.T) {
	// Non-PathError: false.
	if isSymlinkOpenError(errors.New("plain")) {
		t.Error("plain error → true; want false")
	}
	// PathError with non-symlink errno: false.
	if isSymlinkOpenError(&os.PathError{Op: "open", Err: syscall.EACCES}) {
		t.Error("EACCES → true; want false")
	}
	// PathError with ELOOP: true.
	if !isSymlinkOpenError(&os.PathError{Op: "open", Err: syscall.ELOOP}) {
		t.Error("ELOOP → false; want true")
	}
}

func TestScenario_BindingEvaluator_WithEnvAndWorkingDir(t *testing.T) {
	b := &BindingEvaluator{}
	WithEnv("FOO=bar", "BAZ=qux")(b)
	WithWorkingDir("/tmp/x")(b)
	WithInheritEnv("HOME", "USER")(b)
	if len(b.Env) != 2 || b.Env[0] != "FOO=bar" {
		t.Errorf("Env = %v", b.Env)
	}
	if b.WorkingDir != "/tmp/x" {
		t.Errorf("WorkingDir = %q", b.WorkingDir)
	}
	if len(b.InheritEnv) != 2 {
		t.Errorf("InheritEnv = %v", b.InheritEnv)
	}
}
