package runner

import (
	"errors"
	"testing"
	"time"
)

func TestScenario_Pass_OpenAcquiresLockAndPublishesEvent(t *testing.T) {
	tbl := NewRoleContextLockTable()
	bus := NewOperatorBus()
	var events []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { events = append(events, e) })

	p, err := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl, Bus: bus,
	})
	if err != nil {
		t.Fatalf("OpenPass: %v", err)
	}
	defer p.Close("done")

	if got, _ := tbl.InspectHolder("analyst", "checkout"); got != "P1" {
		t.Fatalf("lock holder = %q; want P1", got)
	}
	if p.State() != PassStateOpen {
		t.Fatalf("State = %q; want open", p.State())
	}
	if len(events) != 1 || events[0].Kind != OpEventPassOpened {
		t.Fatalf("events = %+v; want one pass-opened", events)
	}
}

func TestScenario_Pass_CloseReleasesLockAndPublishesEvent(t *testing.T) {
	tbl := NewRoleContextLockTable()
	bus := NewOperatorBus()
	var events []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { events = append(events, e) })

	p, _ := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl, Bus: bus,
	})
	p.Close("clauses-evaluated")

	if _, held := tbl.InspectHolder("analyst", "checkout"); held {
		t.Fatal("lock still held after Close")
	}
	if p.State() != PassStateClosed {
		t.Fatalf("State = %q; want closed", p.State())
	}
	if p.CloseReason() != "clauses-evaluated" {
		t.Fatalf("CloseReason = %q; want 'clauses-evaluated'", p.CloseReason())
	}
	if p.ClosedAt().IsZero() {
		t.Fatal("ClosedAt should be stamped after Close")
	}
	// Two events: opened + closed.
	if len(events) != 2 {
		t.Fatalf("events = %d; want 2", len(events))
	}
	if events[1].Kind != OpEventPassClosed {
		t.Fatalf("second event = %q; want pass-closed", events[1].Kind)
	}
}

func TestScenario_Pass_AbortMarksAborted(t *testing.T) {
	tbl := NewRoleContextLockTable()
	p, _ := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl,
	})
	p.Abort("evaluator-panic")
	if p.State() != PassStateAborted {
		t.Fatalf("State = %q; want aborted", p.State())
	}
	if _, held := tbl.InspectHolder("analyst", "checkout"); held {
		t.Fatal("lock still held after Abort")
	}
}

func TestScenario_Pass_CloseIdempotent(t *testing.T) {
	tbl := NewRoleContextLockTable()
	p, _ := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl,
	})
	p.Close("first")
	p.Close("second") // no-op
	if got := p.CloseReason(); got != "first" {
		t.Fatalf("CloseReason = %q; want 'first' (second Close should be no-op)", got)
	}
}

func TestScenario_Pass_OpenBusyReturnsBusyError(t *testing.T) {
	tbl := NewRoleContextLockTable()
	p1, _ := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl,
	})
	defer p1.Close("done")

	_, err := OpenPass(PassOptions{
		PassID: "P2", Role: "analyst", Context: "checkout",
		ArrowID: "A2", LockTable: tbl,
	})
	var busy *ErrRoleContextBusy
	if !errors.As(err, &busy) {
		t.Fatalf("expected *ErrRoleContextBusy; got %v", err)
	}
}

func TestScenario_Pass_OpenValidatesInputs(t *testing.T) {
	tbl := NewRoleContextLockTable()
	cases := []struct {
		name string
		opts PassOptions
		want error
	}{
		{"empty PassID", PassOptions{Role: "a", Context: "c", ArrowID: "A1", LockTable: tbl}, ErrPassIDEmpty},
		{"empty Role", PassOptions{PassID: "P", Context: "c", ArrowID: "A1", LockTable: tbl}, ErrPassRoleEmpty},
		{"empty Context", PassOptions{PassID: "P", Role: "a", ArrowID: "A1", LockTable: tbl}, ErrPassContextEmpty},
		{"empty ArrowID", PassOptions{PassID: "P", Role: "a", Context: "c", LockTable: tbl}, ErrPassArrowEmpty},
		{"nil LockTable", PassOptions{PassID: "P", Role: "a", Context: "c", ArrowID: "A1"}, ErrPassLockTableNil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := OpenPass(c.opts)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v; want %v", err, c.want)
			}
		})
	}
}

func TestScenario_Pass_BusOptional_NoBusNoPanic(t *testing.T) {
	tbl := NewRoleContextLockTable()
	p, err := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl, Bus: nil,
	})
	if err != nil {
		t.Fatalf("OpenPass with nil bus: %v", err)
	}
	p.Close("done")
	if p.State() != PassStateClosed {
		t.Fatalf("State = %q; want closed", p.State())
	}
}

func TestScenario_Pass_NowOverrideStampsOpened(t *testing.T) {
	tbl := NewRoleContextLockTable()
	pinned := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	p, _ := OpenPass(PassOptions{
		PassID: "P1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: tbl,
		Now: func() time.Time { return pinned },
	})
	defer p.Close("done")
	if !p.OpenedAt().Equal(pinned) {
		t.Fatalf("OpenedAt = %v; want %v", p.OpenedAt(), pinned)
	}
}
