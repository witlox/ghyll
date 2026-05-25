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

// TestStartSession_OversizedOpID_5000Runes pins the wire form for
// the "(string of length 5000)" meta-descriptor row that lived in
// attestation.feature's deferred op-id outline. Gherkin can't
// materialize a 5000-rune table cell; the contract is asserted
// here against the canonical validator instead.
func TestStartSession_OversizedOpID_5000Runes(t *testing.T) {
	huge := strings.Repeat("a", 5000)
	_, err := StartSession(huge)
	if !errors.Is(err, ErrOpIDTooLong) {
		t.Errorf("StartSession(5000-rune) error = %v; want ErrOpIDTooLong", err)
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
	// Escape sequence keeps the source file itself free of literal
	// bidi-control characters (CVE-2021-42574, staticcheck ST1018).
	c := "alice\u202eevil"
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

func TestStartSession_LengthCheckIsOnTrimmedNotRaw(t *testing.T) {
	// validation-pass-1 finding #10: length should be measured on the
	// trimmed+normalized form, not the raw input. Leading/trailing
	// whitespace must not consume the byte budget.
	// Build "  " + (MaxOpIDLength) characters + "  " — total bytes
	// > MaxOpIDLength, but the trimmed form fits.
	raw := "  " + strings.Repeat("a", MaxOpIDLength) + "  "
	s, err := StartSession(raw)
	if err != nil {
		t.Errorf("StartSession with trimmable-length string should succeed; got %v", err)
	}
	if s != nil && s.OpID() != strings.Repeat("a", MaxOpIDLength) {
		t.Errorf("OpID should be the trimmed form; got %q", s.OpID())
	}
}

func TestStartSession_NFCNormalization(t *testing.T) {
	// validation-pass-1 finding #12: NFC-normalize so the same logical
	// identity in different encoding forms yields the same op-id.
	// "é" can be U+00E9 (composed) OR U+0065 U+0301 (decomposed).
	composed := "alicé"    // alicé (one char é)
	decomposed := "alicé" // alice + combining acute
	s1, err := StartSession(composed)
	if err != nil {
		t.Fatalf("composed form should validate: %v", err)
	}
	s2, err := StartSession(decomposed)
	if err != nil {
		t.Fatalf("decomposed form should validate: %v", err)
	}
	if s1.OpID() != s2.OpID() {
		t.Errorf("NFC should canonicalize: composed=%q decomposed=%q (normalized forms should match)",
			s1.OpID(), s2.OpID())
	}
}

func TestStartSession_BlocksTrojanSourceBidi(t *testing.T) {
	// validation-pass-1 finding #11: block all CVE-2021-42574 bidi
	// controls, not just RTL override. Use numeric runes — literal
	// invisible characters in source files are confusing to readers.
	cases := map[string]rune{
		"LRM": 0x200E,
		"RLM": 0x200F,
		"LRE": 0x202A,
		"RLE": 0x202B,
		"PDF": 0x202C,
		"LRO": 0x202D,
		"LRI": 0x2066,
		"RLI": 0x2067,
		"FSI": 0x2068,
		"PDI": 0x2069,
	}
	for name, r := range cases {
		opID := "alice" + string(r) + "bob"
		_, err := StartSession(opID)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("%s (U+%04X) should be rejected; got %v", name, r, err)
		}
	}
}

func TestStartSession_BlocksZeroWidthCharacters(t *testing.T) {
	// Invisible / zero-width chars per validation-pass-1 finding #11.
	// Using numeric runes — Go source forbids literal BOM and some
	// linters flag the others, so name them by code point.
	cases := map[string]rune{
		"ZWSP": 0x200B,
		"ZWNJ": 0x200C,
		"ZWJ":  0x200D,
		"WJ":   0x2060,
		"BOM":  0xFEFF,
	}
	for name, r := range cases {
		opID := "alice" + string(r) + "bob"
		_, err := StartSession(opID)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("%s (U+%04X) should be rejected; got %v", name, r, err)
		}
	}
}

func TestStartSession_BlocksC1Controls(t *testing.T) {
	// C1 control range U+0080..U+009F also blocked (in addition to C0 + DEL).
	cases := []rune{0x7F, 0x80, 0x9F}
	for _, r := range cases {
		opID := "alice" + string(r) + "bob"
		_, err := StartSession(opID)
		if !errors.Is(err, ErrOpIDInvalidCharacters) {
			t.Errorf("control U+%04X should be rejected; got %v", r, err)
		}
	}
}

func TestValidateAndNormalizeOpID_ReturnsNormalizedForm(t *testing.T) {
	// Public helper for callers that want the normalized form without
	// creating a session.
	normalized, err := ValidateAndNormalizeOpID("  alice  ")
	if err != nil {
		t.Fatalf("ValidateAndNormalizeOpID returned err: %v", err)
	}
	if normalized != "alice" {
		t.Errorf("normalized = %q; want %q", normalized, "alice")
	}
}
