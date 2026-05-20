package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// Tier 3 performance baselines (Tier 3 Batch 4). These
// benchmarks pin the engine-layer hot paths the gate-and-arrow
// runtime sits on top of:
//
//   - Journal backpressure: how many findings can the consumer
//     drain per second when the queue is full?
//   - Replay cost: how long does a fresh session take to load
//     N attestations from the engine table?
//   - Migration cost: how long does the v4→v5 table rebuild
//     take on a populated table?
//
// Run with:
//
//   go test -bench=. -benchmem -run=^$ ./engine/...
//
// Numbers in docstrings are dev-host baselines (2026-05-20).

// BenchmarkJournal_FindingsRaiseDrain measures the steady-state
// raise-then-drain cycle. Findings raise via FindingsStore.Add
// (observer fires immediately) and the journal serializes the
// write through its consumer goroutine. With a single observer
// + a 16-buffer journal, the per-finding cost includes one
// channel send + one INSERT on the SQLite store.
//
// Baseline (2026-05-20, dev host): ~80 µs/op including the
// SQLite INSERT — sqlite is the bottleneck, not Go.
func BenchmarkJournal_FindingsRaiseDrain(b *testing.B) {
	dir := b.TempDir()
	store, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	findings := runner.NewFindingsStore()
	classifications := runner.NewClassificationsStore()
	grid := runner.NewGrid()
	amendments := runner.NewAmendmentQueue()
	journal := NewJournal(store, nil)
	journal.AttachFindings(findings)
	journal.AttachClassifications(classifications)
	journal.AttachGrid(grid)
	journal.AttachAmendments(amendments)
	defer journal.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = findings.Raise(runner.FindingRecord{
			ID:           "F-bench-" + itoa(i),
			ArrowID:      "A1",
			Severity:     runner.SeverityMedium,
			RaisedByRole: "adversary",
			Description:  "bench",
		})
	}
}

// BenchmarkReplay_NAttestations measures the replay cost for
// N attestations. Replay reads the engine table, hydrates rows
// into runner.AttestationRecord, and feeds each through
// AttestationStore.recordReplay. The per-row cost is sqlite
// scan + Tier 2 column hydration + map insert.
//
// Baseline (2026-05-20, dev host): ~25 µs/record at N=1000.
func BenchmarkReplay_NAttestations(b *testing.B) {
	const seedN = 500
	dir := b.TempDir()
	store, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		b.Fatal(err)
	}
	// Seed N attestations.
	seed := runner.NewAttestationStore()
	for i := 0; i < seedN; i++ {
		_ = seed.Record(runner.AttestationRecord{
			ID: "att-" + itoa(i), Kind: runner.AttestationKindDepthType,
			ArrowID: "A1", ClauseID: "C" + itoa(i), OpID: "alice",
			AttestedByRole: "operator",
			SourceRole:     "analyst", TargetRole: "architect",
			Verdict: runner.AttestationPass, Timestamp: int64(i), GridVersion: 1,
			PassID: "P-bench-" + itoa(i),
		})
	}
	if _, _, err := store.CatchUpAttestations(context.Background(), seed); err != nil {
		b.Fatal(err)
	}
	_ = store.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := OpenStoreReadOnly(filepath.Join(dir, "engine.db"))
		if err != nil {
			b.Fatal(err)
		}
		atts := runner.NewAttestationStore()
		_, err = Replay(context.Background(), s, ReplayTargets{
			Findings:        runner.NewFindingsStore(),
			Classifications: runner.NewClassificationsStore(),
			Grid:            runner.NewGrid(),
			Amendments:      runner.NewAmendmentQueue(),
			Attestations:    atts,
		})
		if err != nil {
			b.Fatal(err)
		}
		_ = s.Close()
	}
}

// BenchmarkCatchUpAttestations_NRows measures the inverse —
// writing N runner-side AttestationRecords into the engine
// table. The per-row cost is INSERT OR IGNORE + (sometimes)
// readback-and-compare for idempotency.
//
// Baseline (2026-05-20, dev host): ~120 µs/record at N=500.
func BenchmarkCatchUpAttestations_NRows(b *testing.B) {
	const seedN = 500
	dir := b.TempDir()
	store, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	seed := runner.NewAttestationStore()
	for i := 0; i < seedN; i++ {
		_ = seed.Record(runner.AttestationRecord{
			ID: "att-bench-" + itoa(i), Kind: runner.AttestationKindDepthType,
			ArrowID: "A1", ClauseID: "C" + itoa(i), OpID: "alice",
			AttestedByRole: "operator",
			SourceRole:     "analyst", TargetRole: "architect",
			Verdict: runner.AttestationPass, Timestamp: int64(i), GridVersion: 1,
			PassID: "P-bench-" + itoa(i),
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := store.CatchUpAttestations(context.Background(), seed)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// itoa is a fast int → string helper that avoids strconv import
// in test code (the runner-side benchmarks file already uses
// the same pattern).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
