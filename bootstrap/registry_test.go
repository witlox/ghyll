package bootstrap

import (
	"errors"
	"strings"
	"testing"
)

func TestSessionRegistry_DeclareThenActive(t *testing.T) {
	r := NewSessionRegistry()
	if got := r.Active(); got != nil {
		t.Errorf("Active() on empty registry = %v; want nil", got)
	}
	sess, err := r.Declare("alice@example.com")
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if !sess.Active() {
		t.Error("returned session should be active")
	}
	if got := r.ActiveOpID(); got != "alice@example.com" {
		t.Errorf("ActiveOpID() = %q; want alice@example.com", got)
	}
}

func TestSessionRegistry_SecondDeclareRefused(t *testing.T) {
	// Scenario 212: while Alice is active, Bob's declare must be
	// refused with ErrSessionAlreadyActive and the error names Alice.
	r := NewSessionRegistry()
	if _, err := r.Declare("alice@example.com"); err != nil {
		t.Fatal(err)
	}
	_, err := r.Declare("bob@example.com")
	if err == nil {
		t.Fatal("expected error on second declare; got nil")
	}
	if !errors.Is(err, ErrSessionAlreadyActive) {
		t.Errorf("err = %v; want ErrSessionAlreadyActive", err)
	}
	if !strings.Contains(err.Error(), "alice@example.com") {
		t.Errorf("error %q should name the active op-id", err)
	}
	// Bob's declare must NOT have replaced Alice.
	if got := r.ActiveOpID(); got != "alice@example.com" {
		t.Errorf("active op-id changed to %q; expected Alice to remain", got)
	}
}

func TestSessionRegistry_CloseThenRedeclare(t *testing.T) {
	r := NewSessionRegistry()
	if _, err := r.Declare("alice"); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if r.Active() != nil {
		t.Error("Active() should be nil after Close")
	}
	// Now Bob can declare.
	if _, err := r.Declare("bob"); err != nil {
		t.Fatalf("Declare(bob) after Close(alice): %v", err)
	}
	if got := r.ActiveOpID(); got != "bob" {
		t.Errorf("ActiveOpID() = %q; want bob", got)
	}
	// Alice's session is in history.
	hist := r.History()
	if len(hist) != 1 {
		t.Fatalf("History len = %d; want 1", len(hist))
	}
	if hist[0].OpID() != "alice" {
		t.Errorf("history[0] op-id = %q; want alice", hist[0].OpID())
	}
}

func TestSessionRegistry_CloseIdempotent(t *testing.T) {
	r := NewSessionRegistry()
	r.Close() // empty registry — no-op
	r.Close()
	if r.Active() != nil {
		t.Error("Active() after no-op Close should be nil")
	}
	// Now declare and double-close.
	_, _ = r.Declare("alice")
	r.Close()
	r.Close()
	if r.Active() != nil {
		t.Error("Active() after double Close should be nil")
	}
}

func TestSessionRegistry_DeclareRejectsInvalidOpID(t *testing.T) {
	// Registry uses StartSession; op-id validation errors propagate.
	r := NewSessionRegistry()
	if _, err := r.Declare(""); !errors.Is(err, ErrOpIDRequired) {
		t.Errorf("empty op-id: got %v; want ErrOpIDRequired", err)
	}
	if r.ActiveOpID() != "" {
		t.Error("failed declare must not leave registry holding a session")
	}
	if _, err := r.Declare("path/with/slash"); !errors.Is(err, ErrOpIDInvalidCharacters) {
		t.Errorf("path-traversal op-id: got %v; want ErrOpIDInvalidCharacters", err)
	}
}

func TestSessionRegistry_SurvivesReEntry(t *testing.T) {
	// Scenario 205: op-id declared once survives init re-entry. The
	// registry holds the same Session across re-entries; "re-enter"
	// is just a logical concept — the runtime calls something like
	// "init re-enters" but doesn't redeclare the session.
	r := NewSessionRegistry()
	sess1, err := r.Declare("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate init suspending for missing-binding and re-entering:
	// nothing happens to the session. The same session pointer is
	// still active.
	sess2 := r.Active()
	if sess2 != sess1 {
		t.Error("re-entry should preserve the same session pointer")
	}
	if sess2.OpID() != "alice@example.com" {
		t.Errorf("re-entry op-id changed to %q", sess2.OpID())
	}
}

func TestSessionRegistry_EndOnSessionDirectlyAlsoFreesRegistry(t *testing.T) {
	// If a caller bypasses Close() and calls End() on the session
	// directly, the registry's Active() filters it out (Active
	// checks the underlying session's Active() flag). Declare can
	// then succeed for a new op-id.
	r := NewSessionRegistry()
	sess, _ := r.Declare("alice")
	sess.End()
	if r.Active() != nil {
		t.Error("Active() after session.End() should be nil")
	}
	// A second Declare now succeeds. The first session does NOT
	// land in History() since we bypassed Close().
	if _, err := r.Declare("bob"); err != nil {
		t.Errorf("Declare(bob) after session.End(): %v", err)
	}
	if len(r.History()) != 0 {
		t.Errorf("History should be empty (we bypassed Close); got %d", len(r.History()))
	}
}
