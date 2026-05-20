package runner

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Benchmarks pin the hot-path performance characteristics of the
// Tier-1 components. Run with:
//
//   go test -bench=. -benchmem -run=^$ ./runner/...
//
// These are not regression tests (no targets are encoded in test
// assertions); they are characterization benchmarks the operator
// runs periodically to detect drift. The numbers in the docstrings
// below capture the development-host baseline as of 2026-05.

// BenchmarkAttestationStore_Record measures the steady-state cost
// of recording one attestation. The store's hot path: validate +
// dedup-check + map insert + observer fanout. No observers
// registered here, so this measures the floor.
func BenchmarkAttestationStore_Record(b *testing.B) {
	rec := AttestationRecord{
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1,
		GridVersion:    1,
		PassID:         "P-bench",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewAttestationStore()
		r := rec
		r.ID = "att-A1-C1-v1"
		_ = s.Record(r)
	}
}

// BenchmarkAttestationStore_Lookup measures the read path. One
// record pre-recorded; Lookup hits the map under an RWMutex.
func BenchmarkAttestationStore_Lookup(b *testing.B) {
	s := NewAttestationStore()
	rec := AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1, PassID: "P-bench",
	}
	_ = s.Record(rec)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Lookup("att-A1-C1-v1")
	}
}

// BenchmarkRoleContextLockTable_AcquireRelease measures the lock-
// table hot path: one TryAcquire + Release pair per iteration on a
// unique (role, context) tuple so each goroutine sees fresh state.
func BenchmarkRoleContextLockTable_AcquireRelease(b *testing.B) {
	tbl := NewRoleContextLockTable()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tok, err := tbl.TryAcquire("analyst", "checkout", "P", 0)
		if err != nil {
			b.Fatal(err)
		}
		tok.Release()
	}
}

// BenchmarkOperatorBus_Publish measures the bus's fanout cost
// with one subscriber that no-ops. The publisher hits an RWMutex
// for the snapshot, allocates a slice copy, and calls the
// subscriber synchronously.
func BenchmarkOperatorBus_Publish(b *testing.B) {
	bus := NewOperatorBus()
	bus.Subscribe(func(_ OperatorEvent) {})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(OperatorEvent{Kind: OpEventPassOpened, ArrowID: "A1"})
	}
}

// BenchmarkOperatorBus_PublishConcurrent measures the bus under
// concurrent publishers (the realistic case where the journal +
// dispatcher publish in parallel goroutines).
func BenchmarkOperatorBus_PublishConcurrent(b *testing.B) {
	bus := NewOperatorBus()
	bus.Subscribe(func(_ OperatorEvent) {})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bus.Publish(OperatorEvent{Kind: OpEventPassOpened, ArrowID: "A1"})
		}
	})
}

// BenchmarkInsufficientBasisTracker_Record measures the tracker's
// per-verdict cost: map lookup + increment + threshold check +
// (rarely) bus publish.
func BenchmarkInsufficientBasisTracker_Record(b *testing.B) {
	bus := NewOperatorBus()
	tr := NewInsufficientBasisTracker(1_000_000, bus) // high max → never escalate
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Record("A1", "C5", AttestationInsufficientBasis)
	}
}

// BenchmarkPass_OpenClose measures the full Pass lifecycle:
// OpenPass (lock acquire + struct init + optional bus publish)
// + Close (lock release + bus publish).
func BenchmarkPass_OpenClose(b *testing.B) {
	tbl := NewRoleContextLockTable()
	bus := NewOperatorBus()
	bus.Subscribe(func(_ OperatorEvent) {})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err := OpenPass(PassOptions{
			PassID: "P", Role: "analyst", Context: "checkout",
			ArrowID: "A1", LockTable: tbl, Bus: bus,
		})
		if err != nil {
			b.Fatal(err)
		}
		p.Close("done")
	}
}

// BenchmarkLockContention_64Goroutines measures the lock table
// under heavy concurrent contention on a single (role, context)
// tuple. Each goroutine attempts to acquire; one succeeds + holds
// briefly, others see ErrRoleContextBusy.
func BenchmarkLockContention_64Goroutines(b *testing.B) {
	tbl := NewRoleContextLockTable()
	const goroutines = 64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for j := 0; j < goroutines; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok, err := tbl.TryAcquire("analyst", "checkout", "P", 0)
				if err == nil {
					tok.Release()
				}
			}()
		}
		wg.Wait()
	}
}

// BenchmarkDispatcher_ClauseEval measures the dispatcher driving
// one arrow with one clause through Runner.Evaluate. Captures the
// pass open/close + clause eval round-trip including the journal
// observer fanout (no journal attached here — measures the
// runner-only floor).
func BenchmarkDispatcher_ClauseEval(b *testing.B) {
	reg := NewRegistry()
	RegisterBuiltins(reg)
	runner := NewRunner(reg).WithActualTier(DepthRankRealistic)
	tbl := NewRoleContextLockTable()
	passes := NewPassRegistry()
	d := &PassDispatcher{
		LockTable:     tbl,
		Passes:        passes,
		RunnerFactory: func(_ DepthRank) *Runner { return runner },
		PassIDGen:     func() string { return "P" },
		Now:           func() time.Time { return time.Time{} },
	}
	arrow := ArrowDefinition{
		ID: "A1", SourceRole: "analyst", TargetRole: "architect",
		Stratum: "L1", Context: "checkout",
		Clauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1",
			Args: map[string]any{"scope": "**", "markers": []any{"TODO"}}}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := d.Dispatch(context.Background(), DispatchRequest{
			Role: "analyst", Context: "checkout",
			Arrow: arrow, ActualTier: DepthRankRealistic,
			ProjectDir: b.TempDir(),
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
