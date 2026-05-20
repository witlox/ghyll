package bootstrap

import "testing"

// Tier 3 coverage push (batch 5) — covers ActiveSnapshot and
// HistoryLen which were at 0% pre-Tier-3. Smaller surface = quick
// wins for the 77 → 80 push.

func TestScenario_SessionRegistry_ActiveSnapshot_RoundTrip(t *testing.T) {
	r := NewSessionRegistry()
	if snap := r.ActiveSnapshot(); snap.Active {
		t.Error("empty registry: ActiveSnapshot.Active = true")
	}
	if _, err := r.Declare("alice@example.com"); err != nil {
		t.Fatal(err)
	}
	snap := r.ActiveSnapshot()
	if !snap.Active {
		t.Error("Active = false after Declare")
	}
	if snap.OpID != "alice@example.com" {
		t.Errorf("OpID = %q; want alice@example.com", snap.OpID)
	}
	if snap.OpIDHash == "" {
		t.Error("OpIDHash empty")
	}
	if len(snap.OpIDHash) != 12 {
		t.Errorf("OpIDHash len = %d; want 12 hex chars", len(snap.OpIDHash))
	}
}

func TestScenario_SessionRegistry_HistoryLen_TracksClosedSessions(t *testing.T) {
	r := NewSessionRegistry()
	if got := r.HistoryLen(); got != 0 {
		t.Errorf("empty: %d; want 0", got)
	}
	for i := 0; i < 3; i++ {
		_, _ = r.Declare("alice@example.com")
		r.Close()
	}
	if got := r.HistoryLen(); got != 3 {
		t.Errorf("after 3 cycles: %d; want 3", got)
	}
}

func TestScenario_SessionRegistry_ActiveSnapshot_AfterClose(t *testing.T) {
	r := NewSessionRegistry()
	_, _ = r.Declare("alice@example.com")
	r.Close()
	if snap := r.ActiveSnapshot(); snap.Active {
		t.Error("snapshot.Active = true after Close")
	}
}
