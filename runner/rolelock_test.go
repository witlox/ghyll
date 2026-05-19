package runner

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScenario_RoleLock_AcquireRelease_Roundtrip(t *testing.T) {
	tbl := NewRoleContextLockTable()
	tok, err := tbl.TryAcquire("analyst", "checkout", "P1", 0)
	if err != nil {
		t.Fatalf("first TryAcquire: %v", err)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d; want 1", tbl.Len())
	}
	if holder, ok := tbl.InspectHolder("analyst", "checkout"); !ok || holder != "P1" {
		t.Fatalf("InspectHolder = (%q, %v); want (P1, true)", holder, ok)
	}
	tok.Release()
	if tbl.Len() != 0 {
		t.Fatalf("Len after release = %d; want 0", tbl.Len())
	}
}

func TestScenario_RoleLock_BusyOnSameTuple_CarriesHolderIdentity(t *testing.T) {
	tbl := NewRoleContextLockTable()
	_, err := tbl.TryAcquire("analyst", "checkout", "P1", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tbl.TryAcquire("analyst", "checkout", "P2", 0)
	var busy *ErrRoleContextBusy
	if !errors.As(err, &busy) {
		t.Fatalf("expected *ErrRoleContextBusy; got %v", err)
	}
	if busy.HoldingPass != "P1" {
		t.Fatalf("HoldingPass = %q; want P1", busy.HoldingPass)
	}
	if busy.Role != "analyst" || busy.Context != "checkout" {
		t.Fatalf("busy role/ctx = (%q, %q); want (analyst, checkout)",
			busy.Role, busy.Context)
	}
	if busy.AcquiredAt.IsZero() {
		t.Fatal("AcquiredAt zero — should record when the existing holder claimed it")
	}
}

func TestScenario_RoleLock_DisjointTuplesAcquireIndependently(t *testing.T) {
	tbl := NewRoleContextLockTable()
	if _, err := tbl.TryAcquire("analyst", "checkout", "P1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.TryAcquire("analyst", "payments", "P2", 0); err != nil {
		t.Fatalf("disjoint context should not collide: %v", err)
	}
	if _, err := tbl.TryAcquire("architect", "checkout", "P3", 0); err != nil {
		t.Fatalf("disjoint role should not collide: %v", err)
	}
	if tbl.Len() != 3 {
		t.Fatalf("Len = %d; want 3", tbl.Len())
	}
}

func TestScenario_RoleLock_ReleaseIdempotent(t *testing.T) {
	tbl := NewRoleContextLockTable()
	tok, err := tbl.TryAcquire("analyst", "checkout", "P1", 0)
	if err != nil {
		t.Fatal(err)
	}
	tok.Release()
	tok.Release()
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d; want 0 after idempotent release", tbl.Len())
	}
}

func TestScenario_RoleLock_StaleReleaseDoesNotClobberNewHolder(t *testing.T) {
	tbl := NewRoleContextLockTable()
	tok1, _ := tbl.TryAcquire("analyst", "checkout", "P1", 0)
	tok1.Release()
	tok2, err := tbl.TryAcquire("analyst", "checkout", "P2", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Late release from the previous holder must not drop P2's lock.
	tok1.Release()
	if holder, ok := tbl.InspectHolder("analyst", "checkout"); !ok || holder != "P2" {
		t.Fatalf("InspectHolder = (%q, %v); want (P2, true) — stale release clobbered the new holder",
			holder, ok)
	}
	_ = tok2
}

func TestScenario_RoleLock_TTL_StaleEntrySweptOnNextAcquire(t *testing.T) {
	clock := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	tbl := NewRoleContextLockTable().WithClock(func() time.Time { return clock })
	if _, err := tbl.TryAcquire("analyst", "checkout", "P1", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(200 * time.Millisecond)
	tok2, err := tbl.TryAcquire("analyst", "checkout", "P2", 0)
	if err != nil {
		t.Fatalf("expected stale entry to be swept; got %v", err)
	}
	if holder, _ := tbl.InspectHolder("analyst", "checkout"); holder != "P2" {
		t.Fatalf("InspectHolder after auto-expire = %q; want P2", holder)
	}
	tok2.Release()
}

func TestScenario_RoleLock_ExpireOlderThan_ClearsStaleNotZeroTTL(t *testing.T) {
	clock := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	tbl := NewRoleContextLockTable().WithClock(func() time.Time { return clock })
	_, _ = tbl.TryAcquire("analyst", "checkout", "P1", 100*time.Millisecond)
	_, _ = tbl.TryAcquire("architect", "payments", "P2", 0)
	clock = clock.Add(200 * time.Millisecond)
	expired := tbl.ExpireOlderThan(clock)
	if expired != 1 {
		t.Fatalf("ExpireOlderThan returned %d; want 1", expired)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len after sweep = %d; want 1 (only zero-TTL entry remains)", tbl.Len())
	}
	if holder, _ := tbl.InspectHolder("architect", "payments"); holder != "P2" {
		t.Fatalf("zero-TTL entry was wrongly swept; InspectHolder = %q", holder)
	}
}

func TestScenario_RoleLock_EmptyInputsRejected(t *testing.T) {
	tbl := NewRoleContextLockTable()
	cases := []struct {
		name             string
		role, ctx, pass  string
		expectedFragment string
	}{
		{"empty role", "", "checkout", "P1", "role"},
		{"empty context", "analyst", "", "P1", "context"},
		{"empty passID", "analyst", "checkout", "", "passID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tbl.TryAcquire(c.role, c.ctx, c.pass, 0)
			if err == nil {
				t.Fatalf("%s should error", c.name)
			}
			if !strings.Contains(err.Error(), c.expectedFragment) {
				t.Fatalf("error %q should mention %q", err.Error(), c.expectedFragment)
			}
		})
	}
}

// TestScenario_RoleLock_ConcurrentContention uses a barrier channel
// so every goroutine is blocked-then-released together. Without the
// barrier, the first goroutine could acquire-and-release before the
// second one starts, collapsing the test into sequential stress.
// With the barrier, all 64 goroutines hit TryAcquire while others
// are also hitting it — exercising the mutex contention path.
func TestScenario_RoleLock_ConcurrentContention_OneWinsOthersBusy(t *testing.T) {
	tbl := NewRoleContextLockTable()
	const n = 64
	var wg sync.WaitGroup
	var counterMu sync.Mutex
	successes := 0
	busyErrs := 0

	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		passID := "P" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		go func(p string) {
			defer wg.Done()
			<-start // hold until release
			tok, err := tbl.TryAcquire("analyst", "checkout", p, 0)
			if err != nil {
				counterMu.Lock()
				var busy *ErrRoleContextBusy
				if errors.As(err, &busy) {
					busyErrs++
				}
				counterMu.Unlock()
				return
			}
			// Hold briefly so other goroutines hit the contended
			// path before this one releases. Without the hold the
			// goroutine can release before its peers even reach
			// TryAcquire, collapsing the test to sequential.
			time.Sleep(500 * time.Microsecond)
			counterMu.Lock()
			successes++
			counterMu.Unlock()
			tok.Release()
		}(passID)
	}
	close(start) // unblock all goroutines simultaneously
	wg.Wait()
	if successes < 1 {
		t.Fatalf("no acquisitions succeeded (%d busy, %d successes)", busyErrs, successes)
	}
	if busyErrs < 1 {
		t.Fatalf("no contention observed under barrier — test isn't exercising the lock (%d successes, %d busy)",
			successes, busyErrs)
	}
	if successes+busyErrs != n {
		t.Fatalf("successes (%d) + busy (%d) != n (%d)", successes, busyErrs, n)
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len at end = %d; want 0 (all released)", tbl.Len())
	}
}

func TestScenario_RoleLock_NegativeTTL_Rejected(t *testing.T) {
	tbl := NewRoleContextLockTable()
	_, err := tbl.TryAcquire("analyst", "checkout", "P1", -1*time.Second)
	if err == nil {
		t.Fatal("negative TTL should error (silent collapse to zero-TTL is a silent contract violation)")
	}
}

func TestScenario_RoleLock_ExpireOlderThan_EmptyTable_ReturnsZero(t *testing.T) {
	tbl := NewRoleContextLockTable()
	if got := tbl.ExpireOlderThan(time.Now()); got != 0 {
		t.Fatalf("ExpireOlderThan on empty table = %d; want 0", got)
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d; want 0", tbl.Len())
	}
}

// TestScenario_RoleLock_ExpirationBoundary_InclusiveAtExactNow —
// when now == expiresAt exactly, the entry IS expired (inclusive
// boundary). The same semantic must hold for TryAcquire's stale-sweep
// and ExpireOlderThan; previously they used different operators
// (`!Before` vs `After`), producing inconsistent behavior at the
// exact-equality edge.
func TestScenario_RoleLock_ExpirationBoundary_InclusiveAtExactNow(t *testing.T) {
	t0 := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	clock := t0
	tbl := NewRoleContextLockTable().WithClock(func() time.Time { return clock })

	if _, err := tbl.TryAcquire("analyst", "checkout", "P1", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Pin clock at exactly expiresAt: now == acquiredAt + 100ms.
	clock = t0.Add(100 * time.Millisecond)

	// TryAcquire on the same key should sweep the stale entry.
	tok, err := tbl.TryAcquire("analyst", "checkout", "P2", 0)
	if err != nil {
		t.Fatalf("at boundary now == expiresAt, TryAcquire should sweep stale; got %v", err)
	}
	tok.Release()

	// ExpireOlderThan must use the same inclusive boundary. Set up
	// a fresh entry and pin clock at its exact expiresAt.
	if _, err := tbl.TryAcquire("analyst", "checkout", "P3", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(50 * time.Millisecond) // now == new expiresAt
	if got := tbl.ExpireOlderThan(clock); got != 1 {
		t.Fatalf("ExpireOlderThan at exact-boundary swept %d; want 1 (inclusive)", got)
	}
}

func TestScenario_RoleLock_BusyError_Message_HumanReadable(t *testing.T) {
	tbl := NewRoleContextLockTable()
	_, _ = tbl.TryAcquire("analyst", "checkout", "P1", 0)
	_, err := tbl.TryAcquire("analyst", "checkout", "P2", 0)
	if err == nil {
		t.Fatal("expected busy error")
	}
	msg := err.Error()
	// Sanity: error message names the role, context, and holding pass
	// so the operator can pinpoint the conflict.
	for _, want := range []string{"role-context-busy", "analyst", "checkout", "P1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

// TestScenario_RoleLock_TokenCarriesPassID — the token surfaces the
// holder identity so telemetry can report it without poking the
// table.
func TestScenario_RoleLock_TokenCarriesPassID(t *testing.T) {
	tbl := NewRoleContextLockTable()
	tok, err := tbl.TryAcquire("analyst", "checkout", "P-XYZ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.PassID() != "P-XYZ" {
		t.Fatalf("Token.PassID = %q; want P-XYZ", tok.PassID())
	}
	tok.Release()
}
