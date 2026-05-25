# Component: grid amendment and global lock

The amendment component is the **serializer** for grid changes. The
integrator can detect cross-context defects that require the analyst
to re-engage and amend the spec (`v2-design.md` §3.7); the result is
a new grid version. Amendments are serialized through a project-wide
write-lock per D22 so concurrent amendments don't produce ambiguous
v(N+1).

> Status: design intent.

---

## Scope

**In scope.** The project-wide write-lock for grid changes. FIFO
queue for amendment requests. Dependency-check against in-flight
passes when an amendment commits. Invalidation propagation to
affected arrows. Atomic v(N+1) write. Coordination with the runner
and the state machine engine for in-flight aborts.

**Out of scope.** Triggering the amendment (the integrator's role).
Producing the amended spec (the analyst's role on re-engagement).
The actual semantic merge of two amendments (each amendment is
typed and self-contained; there is no automatic merge — second
amendment lands against the state produced by the first).

---

## Domain model

| Term | Definition |
|---|---|
| **Amendment** | A request to change the arrow grid. Carries: who requested it (typically integrator → analyst), what changed (which spec artifacts, which arrows), and a new version-id `v(N+1)`. |
| **Amendment queue** | A FIFO queue holding amendment requests waiting for the write-lock. |
| **Write-lock** | A project-wide mutex covering grid writes. Owned by **this component** (D34). Held by the amendment in-progress; other amendments wait. Init takes the lock at end-of-init for the v1 write (D35). |
| **Affected arrow** | An arrow whose declared dependencies (per `gates.md` §2.1) include a spec artifact changed by the amendment, OR an arrow that declared no dependencies (conservative fallback per D22). |
| **In-flight check** | When an amendment is about to commit, the component checks every `running` pass to determine which are affected. |
| **Atomic commit** | The grid file is written atomically (temp + rename). Either the new version is fully recorded or the previous version is unchanged. |
| **Grid version** | Monotonically increasing integer. Used in arrow identity (`gates.md` §7.1a) and finding tags (per D22). |

---

## Invariants

1. **Serialization.** At most one amendment is committing at a time.
   The write-lock enforces this.
2. **Atomic version bump.** Either v(N+1) is fully recorded with
   all affected arrows marked `invalidated`, or the entire amendment
   is rolled back; partial states are not observable.
3. **FIFO ordering.** Amendments queued during a busy period
   commit in arrival order. The second amendment lands against the
   state produced by the first.
4. **In-flight affected passes are aborted.** When an amendment
   commits, every affected `running` pass is signaled to abort with
   `reason: invalidated`. Unaffected passes continue.
5. **Findings carry their grid version.** Findings raised during a
   `running` pass that gets aborted are preserved (per D17) and
   tagged with the grid version they were raised under, so they
   are distinguishable from findings on the new v(N+1) arrow.
6. **No silent invalidation.** If an arrow becomes `invalidated`,
   the amendment's commit record names it explicitly. The grid
   change is auditable.
7. **Conservative fallback.** Arrows that declared no dependencies
   become `invalidated` on every amendment. This makes "I forgot
   to declare deps" visible.
8. **Init is exempt from the queue.** Initialization runs against
   v0 and produces v1; that is not an amendment, it is the first
   write. The init arrow itself takes the lock at the end of init
   to write v1, but no amendment queue exists at that point.

---

## Behaviors (features)

### F-1: Amendment requested and processed

```gherkin
Feature: Single amendment commits cleanly

  Scenario: Integrator triggers an amendment
    Given an integrator pass that found a missing cross-context
        spec for ContextA ↔ ContextB
    When the integrator emits a grid-amendment request
    Then the amendment component receives:
      - the requesting pass-id (integrator on ContextA)
      - the target spec artifacts (cross-context/A-B.md must be
        created/updated)
      - a list of arrows that the amendment expects to invalidate
        (or empty, leaving the conservative-fallback logic to
        decide)
    And the amendment is added to the queue

  Scenario: Amendment processed in FIFO order
    Given the queue has one amendment ahead of the new one
    When the head amendment commits and releases the lock
    Then the new amendment acquires the lock
    And processes against the state produced by the prior
        amendment

  Scenario: Amendment commits successfully
    Given an amendment holds the lock
    When the analyst re-runs (re-engaged) and produces the
        amended spec
    And the amendment proposes v(N+1) with the new arrow grid
    Then the component:
      1. computes the set of affected arrows (declared deps that
         match the changed artifacts, plus all no-deps arrows)
      2. signals all affected `running` passes to abort
      3. writes the new grid atomically
      4. records the v(N+1) commit log entry
      5. releases the lock
    And the next queued amendment may proceed
```

### F-2: Dependency-check against in-flight passes

```gherkin
Feature: In-flight passes are checked at commit

  Scenario: Affected pass aborts
    Given pass P1 on (implementer, contextA) is running
    And P1's arrow declared `dependencies: [{artifact:
        "features/contextA/payment.feature", granularity: section,
        on-change: invalidate}]`
    When an amendment changes that file's section
    Then the dep-check identifies P1 as affected
    And signals P1's runner to abort with `reason: invalidated`
    And the arrow's status becomes `invalidated` per §7.2
    And P1's findings are preserved with `grid-version: vN`

  Scenario: Unaffected pass continues
    Given pass P2 on (analyst, contextB) is running
    And P2's arrow declared no dependencies on the amended file
    When the amendment commits
    Then P2 continues running against vN
    And records its completion against vN (not v(N+1))
    And P2's arrow's status reflects the latest completed pass's
        verdict

  Scenario: Conservative fallback for no-deps arrows
    Given pass P3 on (architect, contextB) is running
    And P3's arrow declared no dependencies at all
    When the amendment commits
    Then P3 is treated as affected (conservative fallback per D22)
    And P3 is aborted with `reason: invalidated`
    And the operator-tooling can detect this and flag "P3 should
        have declared dependencies"
```

### F-3: Atomic write

```gherkin
Feature: Grid file write is atomic

  Scenario: Successful atomic write
    Given the amendment is ready to write v(N+1)
    When the component writes the grid
    Then it writes to a temp file (`.ghyll/grid.v(N+1).yaml.tmp`)
    And after successful write, renames to
        `.ghyll/grid.v(N+1).yaml`
    And updates `.ghyll/grid.current` (a symlink or pointer) to
        the new file
    And only after the symlink update is the change visible to
        readers

  Scenario: Crash mid-write
    Given the temp file is partially written
    When the process is killed
    Then on restart, the temp file is unlinked (cleanup)
    And the previous version remains current
    And the amendment is re-queued (or marked failed depending on
        operator policy)
```

### F-4: Concurrent amendments serialize

```gherkin
Feature: Two amendments arriving concurrently

  Scenario: Both queue, second lands against first's output
    Given amendment A1 (changes cross-context/A-B.md) and
        amendment A2 (changes cross-context/B-C.md) arrive at
        roughly the same time
    When the amendment component processes them
    Then both enter the queue
    And A1 commits first (FIFO), producing v(N+1)
    And A2 acquires the lock against state v(N+1)
    And A2 commits, producing v(N+2)
    And the audit log shows v(N+1) and v(N+2) as separate
        commits, not merged

  Scenario: Two amendments touch the same artifact
    Given A1 changes lines 1-20 of `cross-context/A-B.md`
    And A2 changes lines 15-30 of the same file
    When A1 commits and A2 reaches the lock
    Then A2 re-reads the current state of `cross-context/A-B.md`
        (post-A1)
    And the analyst that produced A2 must re-justify the
        amendment against the post-A1 state
    And if the post-A1 state already addresses A2's concern, A2
        becomes a no-op
    And if A2's concern still applies, A2 commits as a separate
        amendment v(N+2)
```

### F-5: Grid version visibility

```gherkin
Feature: Grid version is universally visible

  Scenario: All components read the current grid version
    Given the runner, state machine engine, and operator UI all
        need to know the current grid version
    When any of them queries
    Then they read `.ghyll/grid.current` (or call a get-version
        API)
    And get the same answer regardless of which they query

  Scenario: Pass identity uses grid version
    Given a pass P5 is created on arrow A1
    And the current grid version is v(N+1)
    Then P5's arrow-id is computed as
        `(role-pair, stratum, context, v(N+1))`
    And if the same arrow is re-traversed after v(N+2), the new
        pass has a different arrow-id
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Amendment commit fails partway (disk full, etc.) | Single amendment | Roll back: unlink temp file, leave previous version current, re-queue the amendment (or fail with operator alert depending on retry policy). |
| FM-2 | A pass that should have been aborted misses the signal | Single pass | The pass continues running against vN. When it records completion, the state machine engine detects the version mismatch (pass's arrow-id has stale grid-version) and treats the result as if for `invalidated` arrow; the pass's findings are still preserved with vN tag. |
| FM-3 | Two amendment-component instances run concurrently | Project | Should be impossible (single process per project, per A-4 of `init.md`). If it somehow happens, the OS-level file lock on the temp file prevents simultaneous writes; second one fails with `lock-conflict`. |
| FM-4 | Amendment queue grows unboundedly | Project | If amendments are produced faster than they commit (pathological case), the queue blocks. Operator-tooling should alert on queue length. |
| FM-5 | An amendment proposes changes inconsistent with the schema (e.g., declares an arrow outside the diamond) | Single amendment | The component validates the proposed grid against the schema's invariants (diamond shape, stratum vocabulary, etc.). Invalid proposals are rejected; the analyst that produced the amendment must re-engage. |
| FM-6 | Crash recovery finds a half-committed amendment | Project | On restart, the recovery procedure checks `.ghyll/grid.current` against on-disk grid files. If they disagree, alert; require operator decision. |

---

## Cross-component interactions

- **Amendment ← integrator.** The integrator triggers amendments
  via the `missing-cross-context-spec` finding type. The amendment
  component receives the trigger and enqueues.
- **Amendment ← analyst (re-engaged).** The analyst produces the
  amended spec; the amendment component receives the new arrow grid
  proposal.
- **Amendment → state machine engine.** The component calls the
  engine to set `invalidated` on affected arrows.
- **Amendment → runner.** The component signals affected `running`
  passes to abort.
- **Amendment ↔ checkpoint log.** Each commit produces an entry in
  the checkpoint log: the v(N+1) snapshot, the affected arrows, the
  triggering integrator finding.
- **Amendment ↔ on-disk grid files.** The component owns
  `.ghyll/grid.v*.yaml` files and `.ghyll/grid.current`.

## Diamond v4 wire (ADR-v4-003)

The in-process commit path (`runner.AmendmentCommitter.Commit`)
implements the language-binding-aware variant of the above F-3
contract. See [ADR-v4-003](../../../docs/decisions/v4/003-amendment-driven-re-register-ordering.md)
for the rationale; the commit pseudocode is:

1. Pre-validate via `BindingsReRegister(req)` → registry snapshot
   built from a deep-copy of the live registry plus the amendment's
   `NewLanguageBindings`. Failure here bumps no grid version.
2. Append `req.NewArrows` to the in-memory `runner.Grid`. Partial
   append surfaces as a wrapped error; subsequent steps still run
   so the operator sees the desync (event payload outcome=
   `partial-append-error`).
3. Atomic `swap()` invocation: the snapshot becomes the live
   registry. Concurrent dispatchers see OLD or NEW bindings, never
   partial.
4. Pass-abort: in-flight passes on the SourceArrow abort. The
   `OpEventPassClosed` event fires AFTER the binding swap (I-M-1
   ordering), so subscribers correlating close events to live-
   registry lookups see the contract that superseded the aborted
   pass, not the contract that just died.
5. `Queue.MarkDrained` + journal observer writes the `drained_at`
   timestamp.
6. `OpEventAmendmentDrained` publishes on the bus with a typed
   Payload per [ADR-v4-005](../../../docs/decisions/v4/005-operator-event-typed-payload.md):
   `outcome` ∈ {`complete`, `partial-append-error`,
   `binding-re-register-error`}, `grid_version_before`,
   `grid_version_after`, `arrows_added`, `passes_aborted`.

`/drain-amendments` is the operator-facing trigger for steps 1-6
under the active `/op-id`; the slash command refuses without an
op-id since the audit row carries the operator identity.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Amendments are infrequent enough that the project-wide lock doesn't bottleneck. | Falsifies if a project routinely produces amendments faster than they can commit. Unlikely; amendments require integrator-level work. |
| A-2 | Atomicity via temp-file + rename is sufficient for the project's filesystem. | Falsifies on filesystems where rename isn't atomic (rare but possible — some network filesystems). Init should validate FS guarantees. |
| A-3 | Single-process per project is sufficient. | Falsifies if multi-machine ghyll is needed (per A-4 of `init.md`); the lock would need to be distributed. |
| A-4 | Amendments are typed and analyzable enough to compute the affected set. | Falsifies if some amendments are too freeform to map cleanly to dependency declarations. The conservative-fallback (no-deps arrows always invalidate) absorbs this cost. |

---

## Open questions

- **Selective dependency declaration discovery.** Init's
  auto-propose should encourage operators to declare dependencies
  to minimize conservative-fallback invalidations. A telemetry
  signal showing "X% of your invalidations were conservative" could
  prompt operators to add missing declarations. Out of scope for
  v1.
- **Amendment merge.** Currently two amendments serialize and
  produce two separate version bumps. Could intelligent merging
  collapse them into one v(N+1)? Probably not worth the complexity
  unless amendments become common.
- **Rollback.** Currently amendments only move forward (vN →
  v(N+1)). Should there be a rollback mechanism (v(N+1) →
  back-to-vN)? Useful if an amendment was wrong. Out of scope for
  v1; the operator can produce a counter-amendment v(N+2) that
  reverts the change.
- **Cross-amendment dependency declaration.** An amendment may
  *itself* declare dependencies (e.g., "this amendment depends on
  v(N+1) being a no-op for cross-context A-B"). Not currently in
  the schema; could add if amendments compose in interesting ways.
