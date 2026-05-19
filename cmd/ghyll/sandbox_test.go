package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// clearSandboxEnv unsets every variable that could feed
// DetectSandbox. Use before assertions that need a clean
// "no sandbox" state.
func clearSandboxEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GHYLL_SANDBOX_ASSUME_SAFE",
		"container",
		"KUBERNETES_SERVICE_HOST",
		"FIREJAIL_FILE",
		"SANDBOX_TYPE",
		"GHYLL_REQUIRE_SANDBOX",
	} {
		t.Setenv(k, "")
	}
}

func TestScenario_Sandbox_AssumeSafeOverrideRecognized(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("GHYLL_SANDBOX_ASSUME_SAFE", "running in CI without sandbox; tests only")
	r := DetectSandbox()
	if r.Kind != SandboxAssumed {
		t.Fatalf("Kind = %q; want assumed-safe", r.Kind)
	}
	if !strings.Contains(r.Detail, "running in CI") {
		t.Fatalf("Detail should echo the override reason; got %q", r.Detail)
	}
}

func TestScenario_Sandbox_DockerViaContainerEnv(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("container", "docker")
	r := DetectSandbox()
	if r.Kind != SandboxDocker {
		t.Fatalf("Kind = %q; want docker", r.Kind)
	}
}

func TestScenario_Sandbox_PodmanViaContainerEnv(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("container", "podman")
	if DetectSandbox().Kind != SandboxPodman {
		t.Fatal("podman not detected")
	}
}

func TestScenario_Sandbox_KubernetesViaServiceHost(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if DetectSandbox().Kind != SandboxKubernetes {
		t.Fatal("kubernetes not detected")
	}
}

func TestScenario_Sandbox_FirejailViaEnv(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("FIREJAIL_FILE", "/etc/firejail/ghyll.profile")
	// The firejail check is inside the Linux-specific helper, so
	// only verify on Linux hosts.
	r := DetectSandbox()
	if r.Kind == SandboxFirejail {
		// expected
		return
	}
	// On non-Linux hosts the helper isn't consulted; SandboxNone is
	// acceptable.
	if r.Kind != SandboxNone {
		t.Fatalf("unexpected kind = %q", r.Kind)
	}
}

func TestScenario_Sandbox_EnforcePolicy_WarnsWhenAbsent(t *testing.T) {
	clearSandboxEnv(t)
	var captured bytes.Buffer
	err := EnforceSandboxPolicy(func(s string) { captured.WriteString(s + "\n") })
	if err != nil {
		t.Fatalf("policy without GHYLL_REQUIRE_SANDBOX should warn, not error; got %v", err)
	}
	out := captured.String()
	if !strings.Contains(out, "no sandbox detected") {
		t.Fatalf("warning expected; got %q", out)
	}
}

func TestScenario_Sandbox_EnforcePolicy_RequireFailsWithoutSandbox(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("GHYLL_REQUIRE_SANDBOX", "1")
	var captured bytes.Buffer
	err := EnforceSandboxPolicy(func(s string) { captured.WriteString(s + "\n") })
	if !errors.Is(err, ErrNoSandboxDetected) {
		t.Fatalf("expected ErrNoSandboxDetected; got %v", err)
	}
	if !strings.Contains(captured.String(), "GHYLL_SANDBOX_ASSUME_SAFE") {
		t.Fatalf("error output should mention the bypass var; got %q", captured.String())
	}
}

func TestScenario_Sandbox_EnforcePolicy_RequireOKWhenDetected(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("GHYLL_REQUIRE_SANDBOX", "1")
	t.Setenv("container", "docker")
	var captured bytes.Buffer
	if err := EnforceSandboxPolicy(func(s string) { captured.WriteString(s + "\n") }); err != nil {
		t.Fatalf("policy with sandbox detected should succeed; got %v", err)
	}
	if !strings.Contains(captured.String(), "sandbox detected") {
		t.Fatalf("info line expected; got %q", captured.String())
	}
}

func TestScenario_Sandbox_EnforcePolicy_AssumedSafeFlowsCleanly(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("GHYLL_REQUIRE_SANDBOX", "1")
	t.Setenv("GHYLL_SANDBOX_ASSUME_SAFE", "test harness")
	var captured bytes.Buffer
	if err := EnforceSandboxPolicy(func(s string) { captured.WriteString(s + "\n") }); err != nil {
		t.Fatalf("assume-safe should bypass require; got %v", err)
	}
	if !strings.Contains(captured.String(), "sandbox check bypassed") {
		t.Fatalf("bypass note expected; got %q", captured.String())
	}
}

func TestScenario_Sandbox_TruthyEnvParsing(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"  on":  true,
		"yes":   true,
		"":      false,
		"0":     false,
		"false": false,
		"no":    false,
		"maybe": false,
	}
	for in, want := range cases {
		if got := truthyEnv(in); got != want {
			t.Errorf("truthyEnv(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestScenario_Sandbox_EnforcePolicy_NilOutputCallback(t *testing.T) {
	clearSandboxEnv(t)
	// Nil callback must not panic; policy still returns the right
	// error.
	t.Setenv("GHYLL_REQUIRE_SANDBOX", "1")
	if err := EnforceSandboxPolicy(nil); !errors.Is(err, ErrNoSandboxDetected) {
		t.Fatalf("expected ErrNoSandboxDetected; got %v", err)
	}
}
