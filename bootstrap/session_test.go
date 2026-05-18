package bootstrap

import (
	"errors"
	"strings"
	"testing"
)

func TestStartSession_ValidOpID(t *testing.T) {
	s, err := StartSession("alice@example.com")
	if err != nil {
		t.Fatalf("StartSession with valid op-id returned err: %v", err)
	}
	if s == nil {
		t.Fatal("StartSession returned nil session")
	}
	if !s.Active() {
		t.Error("new session should be Active")
	}
	if s.OpID() != "alice@example.com" {
		t.Errorf("OpID() = %q; want %q", s.OpID(), "alice@example.com")
	}
}

func TestStartSession_EmptyOpID(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"\t\t",
		"\n",
	}
	for _, c := range cases {
		_, err := StartSession(c)
		if !errors.Is(err, ErrOpIDRequired) {
			t.Errorf("StartSession(%q) error = %v; want ErrOpIDRequired", c, err)
		}
	}
}

func TestStartSession_TooLongOpID(t *testing.T) {
	long := strings.Repeat("a", MaxOpIDLength+1)
	_, err := StartSession(long)
	if !errors.Is(err, ErrOpIDTooLong) {
		t.Errorf("StartSession(too-long) error = %v; want ErrOpIDTooLong", err)
	}
}

func TestStartSession_AtMaxLength(t *testing.T) {
	atMax := strings.Repeat("a", MaxOpIDLength)
	if _, err := StartSession(atMax); err != nil {
		t.Errorf("StartSession at MaxOpIDLength should succeed; got %v", err)
	}
}

func TestStartSession_PathSeparator(t *testing.T) {
	cases := []string{
		"alice/bob",
		"/alice",
		"alice/",
		"alice\\bob",
	}
	for _, c := range cases {
		_, err := StartSession(c)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("StartSession(%q) error = %v; want ErrOpIDInvalidCharacters", c, err)
		}
	}
}

func TestStartSession_PathTraversal(t *testing.T) {
	cases := []string{
		"..",
		"../etc/passwd",
		"alice..bob",
		"..alice",
		"alice..",
	}
	for _, c := range cases {
		_, err := StartSession(c)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("StartSession(%q) error = %v; want ErrOpIDInvalidCharacters", c, err)
		}
	}
}

func TestStartSession_ControlCharacters(t *testing.T) {
	cases := []string{
		"alice\x00null", // NUL
		"alice\nbob",    // newline
		"alice\rbob",    // CR
		"alice\tbob",    // tab
		"\x01alice",     // SOH
	}
	for _, c := range cases {
		_, err := StartSession(c)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("StartSession(%q) error = %v; want ErrOpIDInvalidCharacters", c, err)
		}
	}
}

func TestStartSession_UnicodeRTLOverride(t *testing.T) {
	// U+202E RIGHT-TO-LEFT OVERRIDE smuggled into an apparent email.
	c := "alice‮evil"
	_, err := StartSession(c)
	if !errors.Is(err, ErrOpIDInvalidCharacters) {
		t.Errorf("StartSession with RTL override: error = %v; want ErrOpIDInvalidCharacters", err)
	}
}

func TestStartSession_InvalidUTF8(t *testing.T) {
	// Invalid UTF-8 sequence (lone continuation byte).
	c := "alice\x80"
	_, err := StartSession(c)
	if !errors.Is(err, ErrOpIDInvalidCharacters) {
		t.Errorf("StartSession(invalid-utf8) error = %v; want ErrOpIDInvalidCharacters", err)
	}
}

func TestSession_End(t *testing.T) {
	s, err := StartSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Active() {
		t.Fatal("expected new session to be active")
	}
	s.End()
	if s.Active() {
		t.Error("session should not be active after End()")
	}
	// OpID still readable after End for audit.
	if s.OpID() != "alice" {
		t.Errorf("OpID() after End = %q; want \"alice\"", s.OpID())
	}
}

func TestSession_NilSafe(t *testing.T) {
	var s *Session
	if s.Active() {
		t.Error("nil session Active should be false")
	}
	if s.OpID() != "" {
		t.Errorf("nil session OpID = %q; want empty", s.OpID())
	}
	s.End() // must not panic
}

func TestValidateOpID_DoesNotCreateSession(t *testing.T) {
	// Sanity: ValidateOpID is a pure check; doesn't mutate anything.
	if err := ValidateOpID(""); !errors.Is(err, ErrOpIDRequired) {
		t.Errorf("ValidateOpID(\"\") = %v; want ErrOpIDRequired", err)
	}
	if err := ValidateOpID("alice"); err != nil {
		t.Errorf("ValidateOpID(\"alice\") = %v; want nil", err)
	}
}
