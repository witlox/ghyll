# ADR-v4-003: Amendment-driven re-register — in-memory snapshot, atomic swap, fail-before-bump

**Status:** Accepted (2026-05-25)

**Context:**

`AmendmentCommitter.Commit` applies an amendment to the live grid:
appends new arrows, adds new language-binding declarations, aborts
in-flight passes on the source arrow, and persists the new grid to
disk. The order of these steps determines what an observer sees if
any step fails mid-flight.

The v1 ordering (append-then-re-register) opened a window where the
grid was bumped but the registry still pointed at the old bindings.
The first revision moved re-register before grid-append (H5 closure)
but opened a NEW asymmetric window: re-register succeeded but
abort / append errored mid-loop, leaving the live registry holding
new bindings against the old grid version. R10 of the v2 adversarial
flagged this.

**Decision:**

The committer builds an **in-memory snapshot** registry, validates
it, then atomically swaps the snapshot into the live registry only
after the disk write succeeds. Step ordering:

1. Acquire `committer.mu`.
2. Validate FIFO head (R22): the amendment ID must match the queue
   head; on mismatch return `ErrAmendmentCommitFIFO` with the queue
   intact.
3. Build the in-memory registry snapshot via `Registry.Snapshot()`
   + `registerGridBindings(snapshot, overlay, workdir)`. On
   construction failure, abort BEFORE any mutation.
4. Abort in-flight passes whose `ArrowID == amendment.SourceArrow`.
5. Append `NewArrows` to the in-memory grid; bump grid version.
6. Persist the new grid via `bootstrap.Grid.Write(workdir)`.
   - 6a: atomically swap the snapshot into the live registry via
     `snapshot.SwapInto(rt.registry)`. This is the only mutation of
     `rt.registry`; concurrent dispatchers see OLD or NEW, never
     partial.
7. `Queue.MarkDrained(amendment.ID)`.
8. Publish `OpEventAmendmentDrained` with typed Payload.
9. Release `committer.mu`.

Failure at any step ≤ 6 discards the snapshot; the live registry
retains the old bindings; the grid version is unchanged.

**Consequences:**

- The `runner.Registry` gains `Snapshot()` and `SwapInto()` methods.
- `AmendmentCommitter` gains an optional `BindingsReRegister`
  callback that returns a snapshot registry (the implementation
  lives in `cmd/ghyll` per ADR-v4-007).
- Lock order: `committer.mu → AmendmentQueue.mu → Grid.mu`. The
  implementer MUST NOT introduce a reverse path.
- Concurrent dispatch under a drain blocks on the committer's lock
  only at the swap point; reads against the registry are
  lock-protected by `Registry.mu`.
