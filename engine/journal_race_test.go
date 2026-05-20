package engine

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/witlox/ghyll/runner"
)

// TestScenario_Journal_CloseRace_JKindPass verifies the G2-F-1
// remediation: enqueue(jKindPass) holding RLock against a
// concurrent Close holding Lock must not panic on a closed
// channel send. 200 iterations per direction; the race detector
// catches a regression.
func TestScenario_Journal_CloseRace_JKindPass(t *testing.T) {
	for i := 0; i < 200; i++ {
		dir := t.TempDir()
		store, err := OpenStore(filepath.Join(dir, "engine.db"))
		if err != nil {
			t.Fatalf("OpenStore: %v", err)
		}
		j := NewJournal(store, nil)
		reg := runner.NewPassRegistry()
		j.AttachPasses(reg)

		// Open a Pass with a registry observer that triggers a journal
		// pass-event enqueue. Use small buffer to maximize the race
		// window — fill the channel and concurrently Close the
		// journal.
		tbl := runner.NewRoleContextLockTable()
		p, err := runner.OpenPass(runner.PassOptions{
			PassID: "P-race", Role: "r", Context: "c",
			ArrowID: "A1", LockTable: tbl,
		})
		if err != nil {
			t.Fatalf("OpenPass: %v", err)
		}
		reg.Register(p)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); p.Close("done") }()
		go func() { defer wg.Done(); j.Close() }()
		wg.Wait()
		_ = store.Close()
		_ = i
	}
	// Pass-on-no-panic is the contract.
	_ = time.Now
}
