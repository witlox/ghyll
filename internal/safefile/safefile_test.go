package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenario_SafeSegment_RejectsTraversal(t *testing.T) {
	cases := []string{
		"", ".", "..",
		"foo/bar", "foo\\bar", "foo\x00bar",
		"alice..bob", "../../etc/passwd",
	}
	for _, in := range cases {
		if err := SafeSegment(in); !errors.Is(err, ErrSegmentInvalid) {
			t.Errorf("SafeSegment(%q) = %v; want ErrSegmentInvalid", in, err)
		}
	}
}

func TestScenario_SafeSegment_AcceptsBenign(t *testing.T) {
	cases := []string{
		"alice", "alice@example.com", "device-42",
		"ID_1234", "name.with.dots",
	}
	for _, in := range cases {
		if err := SafeSegment(in); err != nil {
			t.Errorf("SafeSegment(%q) = %v; want nil", in, err)
		}
	}
}

func TestScenario_ReadCappedFile_RefusesOversized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCappedFile(path, 50)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("err = %v; want ErrFileTooLarge", err)
	}
}

func TestScenario_ReadCappedFile_AcceptsUnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := ReadCappedFile(path, 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q; want hello", data)
	}
}

func TestScenario_ReadCappedFile_RefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(target, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCappedFile(link, 1000)
	if !errors.Is(err, ErrSymlinkRefused) {
		t.Errorf("err = %v; want ErrSymlinkRefused", err)
	}
}

func TestScenario_ValidateURLScheme(t *testing.T) {
	if err := ValidateURLScheme("https://example.com/x", "https"); err != nil {
		t.Errorf("https: %v", err)
	}
	if err := ValidateURLScheme("http://localhost:8080", "http", "https"); err != nil {
		t.Errorf("http: %v", err)
	}
	if err := ValidateURLScheme("file:///etc/passwd", "https"); !errors.Is(err, ErrSchemeRejected) {
		t.Errorf("file:// → %v; want ErrSchemeRejected", err)
	}
	if err := ValidateURLScheme("data:text/html,evil", "http", "https"); !errors.Is(err, ErrSchemeRejected) {
		t.Errorf("data: → %v; want ErrSchemeRejected", err)
	}
}
