package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Sandbox detection. ghyll executes tool calls (bash, edit, etc.)
// from LLM output directly with no confirmation. Running without
// a sandbox means a compromised model endpoint can execute
// arbitrary code with the user's privileges. The runtime detects
// common sandboxes and either:
//
//   - GHYLL_REQUIRE_SANDBOX unset / "0" / "false":  emits a ⚠
//     warning on stdout and continues (default — usability over
//     enforcement).
//   - GHYLL_REQUIRE_SANDBOX = "1" / "true":  refuses to start
//     when no sandbox is detected. Returns ErrNoSandboxDetected.
//
// Detection is heuristic: we look for the markers each common
// sandbox leaves on the process. Operators in unusual setups can
// override with GHYLL_SANDBOX_ASSUME_SAFE=<reason> (the reason
// surfaces in the warning so the operator's choice is auditable).

// SandboxKind identifies which sandbox was detected, if any.
type SandboxKind string

const (
	SandboxNone        SandboxKind = "none"
	SandboxBubblewrap  SandboxKind = "bubblewrap"
	SandboxSandboxExec SandboxKind = "sandbox-exec"
	SandboxDocker      SandboxKind = "docker"
	SandboxPodman      SandboxKind = "podman"
	SandboxKubernetes  SandboxKind = "kubernetes"
	SandboxLXC         SandboxKind = "lxc"
	SandboxFirejail    SandboxKind = "firejail"
	SandboxAssumed     SandboxKind = "assumed-safe"
)

// SandboxReport is the result of DetectSandbox.
type SandboxReport struct {
	Kind   SandboxKind
	Detail string // human-readable explanation of how we detected
}

// DetectSandbox inspects the process environment and filesystem
// for sandbox markers. Returns SandboxNone if no marker is
// present.
//
// Order: explicit assumed-safe override first (operator opt-out
// is honored), then container env vars (most reliable), then
// per-OS heuristics.
func DetectSandbox() SandboxReport {
	if reason := strings.TrimSpace(os.Getenv("GHYLL_SANDBOX_ASSUME_SAFE")); reason != "" {
		return SandboxReport{
			Kind:   SandboxAssumed,
			Detail: "GHYLL_SANDBOX_ASSUME_SAFE=" + reason,
		}
	}

	// Container env vars — most explicit signal.
	if v := os.Getenv("container"); v != "" {
		switch strings.ToLower(v) {
		case "docker":
			return SandboxReport{Kind: SandboxDocker, Detail: "container=docker"}
		case "podman":
			return SandboxReport{Kind: SandboxPodman, Detail: "container=podman"}
		case "lxc":
			return SandboxReport{Kind: SandboxLXC, Detail: "container=lxc"}
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return SandboxReport{Kind: SandboxKubernetes, Detail: "KUBERNETES_SERVICE_HOST set"}
	}

	// Per-OS filesystem heuristics.
	switch runtime.GOOS {
	case "linux":
		if r := detectLinuxSandbox(); r.Kind != SandboxNone {
			return r
		}
	case "darwin":
		if r := detectDarwinSandbox(); r.Kind != SandboxNone {
			return r
		}
	}

	return SandboxReport{Kind: SandboxNone}
}

// detectLinuxSandbox reads /proc/1/cgroup and /proc/self/status
// for sandbox markers. Returns SandboxNone if none match.
//
// Detection markers:
//   - cgroup containing "docker"  → SandboxDocker
//   - cgroup containing "podman"  → SandboxPodman
//   - cgroup containing "lxc"     → SandboxLXC
//   - status with PID namespace (NStgid != global tgid) → bubblewrap-class
//   - $FIREJAIL_FILE set          → SandboxFirejail
func detectLinuxSandbox() SandboxReport {
	if firejail := os.Getenv("FIREJAIL_FILE"); firejail != "" {
		return SandboxReport{Kind: SandboxFirejail, Detail: "FIREJAIL_FILE=" + firejail}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		text := strings.ToLower(string(data))
		switch {
		case strings.Contains(text, "docker"):
			return SandboxReport{Kind: SandboxDocker, Detail: "/proc/1/cgroup contains docker"}
		case strings.Contains(text, "podman"):
			return SandboxReport{Kind: SandboxPodman, Detail: "/proc/1/cgroup contains podman"}
		case strings.Contains(text, "lxc"):
			return SandboxReport{Kind: SandboxLXC, Detail: "/proc/1/cgroup contains lxc"}
		case strings.Contains(text, "containerd"):
			return SandboxReport{Kind: SandboxDocker, Detail: "/proc/1/cgroup contains containerd"}
		}
	}
	// bubblewrap (and related unprivileged-namespace tooling)
	// puts the process inside a fresh PID namespace. The
	// /proc/self/status NSpid line shows multiple PIDs when
	// inside a namespace.
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "NSpid:") {
				fields := strings.Fields(line)
				// NSpid:<tab><global><tab><namespaced>...
				// More than 2 fields → inside a namespace.
				if len(fields) > 2 {
					return SandboxReport{Kind: SandboxBubblewrap, Detail: "PID namespace detected"}
				}
				break
			}
		}
	}
	return SandboxReport{Kind: SandboxNone}
}

// detectDarwinSandbox looks for sandbox-exec's marker env vars.
// SANDBOX_TYPE is set by `sandbox-exec` when it invokes a child
// process inside a profile.
func detectDarwinSandbox() SandboxReport {
	if t := os.Getenv("SANDBOX_TYPE"); t != "" {
		return SandboxReport{Kind: SandboxSandboxExec, Detail: "SANDBOX_TYPE=" + t}
	}
	return SandboxReport{Kind: SandboxNone}
}

// ErrNoSandboxDetected is returned by EnforceSandboxPolicy when
// GHYLL_REQUIRE_SANDBOX is truthy and no sandbox is detected.
var ErrNoSandboxDetected = errors.New("ghyll: no sandbox detected and GHYLL_REQUIRE_SANDBOX is set")

// EnforceSandboxPolicy implements the runtime's sandbox policy.
// Called once at session start. Returns ErrNoSandboxDetected
// when no sandbox marker is present AND the GHYLL_REQUIRE_SANDBOX
// env var is set to "1" or "true" (case-insensitive). Otherwise
// it emits a friendly summary (warning if no sandbox; informational
// if one is detected) via the provided output callback.
//
// The output callback is the same `s.output` used elsewhere so
// the message flows through ui.Info under normal operation and
// the BDD harness can capture it in tests.
func EnforceSandboxPolicy(out func(string)) error {
	report := DetectSandbox()
	requireSandbox := truthyEnv(os.Getenv("GHYLL_REQUIRE_SANDBOX"))
	switch report.Kind {
	case SandboxNone:
		if requireSandbox {
			if out != nil {
				out(fmt.Sprintf("✗ %v — set GHYLL_SANDBOX_ASSUME_SAFE=<reason> if you know what you're doing",
					ErrNoSandboxDetected))
			}
			return ErrNoSandboxDetected
		}
		if out != nil {
			out("⚠ no sandbox detected — ghyll executes tool calls from the model directly; running unsandboxed is unsafe (set GHYLL_REQUIRE_SANDBOX=1 to refuse this state)")
		}
	case SandboxAssumed:
		if out != nil {
			out(fmt.Sprintf("ℹ sandbox check bypassed: %s", report.Detail))
		}
	default:
		if out != nil {
			out(fmt.Sprintf("ℹ sandbox detected: %s (%s)", report.Kind, report.Detail))
		}
	}
	return nil
}

// truthyEnv returns true for "1", "true", "yes", "on" (any case).
// Empty / unset / anything else returns false.
func truthyEnv(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
