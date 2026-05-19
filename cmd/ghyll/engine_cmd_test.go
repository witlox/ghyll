package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// TestEngineCLI_ParseFlags_Defaults verifies the default --dir + timeout.
func TestEngineCLI_ParseFlags_Defaults(t *testing.T) {
	cwd, _ := os.Getwd()
	fl, err := parseEngineFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, ".ghyll", "engine.db")
	if fl.DBPath != want {
		t.Errorf("DBPath = %q; want %q", fl.DBPath, want)
	}
	if fl.Timeout != defaultEngineCLITimout {
		t.Errorf("Timeout = %v; want %v", fl.Timeout, defaultEngineCLITimout)
	}
}

// TestEngineCLI_ParseFlags_RejectsPositional verifies C8: a trailing
// positional argument surfaces as "unexpected positional argument",
// not "unknown flag".
func TestEngineCLI_ParseFlags_RejectsPositional(t *testing.T) {
	_, err := parseEngineFlags([]string{"trailing"})
	if err == nil {
		t.Fatal("want error for positional arg")
	}
	if !strings.Contains(err.Error(), "positional") {
		t.Errorf("want 'positional' in error; got %v", err)
	}
}

// TestEngineCLI_ParseFlags_DirLengthCap verifies C9: --dir with >
// maxEngineDirLen bytes rejects.
func TestEngineCLI_ParseFlags_DirLengthCap(t *testing.T) {
	long := strings.Repeat("a", maxEngineDirLen+1)
	_, err := parseEngineFlags([]string{"--dir", long})
	if err == nil {
		t.Fatal("want error for oversized --dir")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("want 'exceeds' in error; got %v", err)
	}
}

// TestEngineCLI_ParseFlags_TimeoutNegative verifies --timeout
// validation rejects non-positive values.
func TestEngineCLI_ParseFlags_TimeoutNegative(t *testing.T) {
	_, err := parseEngineFlags([]string{"--timeout", "0"})
	if err == nil {
		t.Fatal("want error for --timeout=0")
	}
}

// TestEngineCLI_PreflightDBPath_DirectoryRejects verifies C6: if
// engine.db is a directory, preflight returns a typed error.
func TestEngineCLI_PreflightDBPath_DirectoryRejects(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "engine.db")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := preflightDBPath(target)
	if err == nil {
		t.Fatal("want error when engine.db is a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("want 'directory' in error; got %v", err)
	}
}

// TestEngineCLI_PreflightDBPath_MissingReturnsNotExist verifies the
// missing-DB sentinel propagates through os.ErrNotExist.
func TestEngineCLI_PreflightDBPath_MissingReturnsNotExist(t *testing.T) {
	err := preflightDBPath(filepath.Join(t.TempDir(), "nope.db"))
	if !os.IsNotExist(err) {
		t.Errorf("want ErrNotExist; got %v", err)
	}
}

// TestEngineCLI_ClassifyError_LockedDB verifies C4: sqlite lock
// errors surface as a friendly message, not the raw sqlite text.
func TestEngineCLI_ClassifyError_LockedDB(t *testing.T) {
	classified := classifyCLIError(&fakeErr{"database is locked at /etc/secret"}, false)
	if strings.Contains(classified.Error(), "/etc/secret") {
		t.Errorf("path leaked: %v", classified)
	}
	if !strings.Contains(classified.Error(), "in use") {
		t.Errorf("want 'in use' message; got %v", classified)
	}
}

// TestEngineCLI_ClassifyError_VerboseMode keeps raw error text.
func TestEngineCLI_ClassifyError_VerboseMode(t *testing.T) {
	classified := classifyCLIError(&fakeErr{"database is locked at /etc/secret"}, true)
	if !strings.Contains(classified.Error(), "/etc/secret") {
		t.Errorf("verbose mode should preserve raw error; got %v", classified)
	}
}

// TestEngineCLI_ClassifyError_SanitizesControlBytes verifies C3:
// control bytes do not flow to the terminal.
func TestEngineCLI_ClassifyError_SanitizesControlBytes(t *testing.T) {
	classified := classifyCLIError(&fakeErr{"some\x1b[31merror\x07with\nnewline"}, false)
	out := classified.Error()
	if strings.ContainsAny(out, "\x1b\x07\n") {
		t.Errorf("control bytes leaked: %q", out)
	}
}

// TestEngineCLI_CountMethods_RoundTrip verifies C1: Count* methods
// return accurate totals beyond the historic 1000-row cap. Writes
// directly to the store to avoid the journal-buffer ceiling that
// would otherwise cap the test at ~1000 rows.
func TestEngineCLI_CountMethods_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.db")
	store, err := engine.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	for i := 0; i < 1100; i++ {
		rec := engine.FindingRecord{
			ID:       "F" + itoa(i),
			ArrowID:  "A1",
			Type:     "local-bug",
			Severity: int(runner.SeverityHigh),
			Status:   "open",
		}
		if err := store.UpsertFinding(ctx, rec); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	n, err := store.CountFindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1100 {
		t.Errorf("CountFindings = %d; want 1100", n)
	}
}

// TestEngineCLI_StructuredMissingMarker verifies C11: the missing-DB
// path emits the marker token.
func TestEngineCLI_StructuredMissingMarker(t *testing.T) {
	if missingEngineLine == "" {
		t.Fatal("missingEngineLine must be non-empty")
	}
	if !strings.Contains(missingEngineLine, "missing") {
		t.Errorf("marker should contain 'missing'; got %q", missingEngineLine)
	}
}

// fakeErr is a tiny error type for table-driven classifyCLIError
// tests.
type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// itoa is a tiny helper to format ints into stable strings without
// pulling fmt into hot paths.
func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b [16]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = digits[i%10]
		i /= 10
	}
	return string(b[pos:])
}
