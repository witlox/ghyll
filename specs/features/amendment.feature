# Implementation: built — runner/amendment.go AmendmentQueue +
# engine/replay.go LoadDrained dedup across restart. Step bindings
# call real runner + engine code.
Feature: Grid amendment and global lock

  # Serializes grid changes through a project-wide write-lock. FIFO
  # amendment queue. Dependency-check against in-flight passes.
  # Atomic v(N+1) commit. Findings preserved with their grid-version tag.
  # See specs/architecture/components/amendment.md.

  # ---- Single amendment commits cleanly ----

  Scenario: Integrator triggers an amendment
    Given an integrator pass that found a missing cross-context spec for ContextA ↔ ContextB
    When the integrator emits a grid-amendment request
    Then the amendment component receives the requesting pass-id, the target spec artifacts, and the affected arrows list (or empty)
    And the amendment is added to the queue

  Scenario: Amendment processed in FIFO order
    Given the queue has one amendment ahead of the new one
    When the head amendment commits and releases the lock
    Then the new amendment acquires the lock
    And processes against the state produced by the prior amendment

  Scenario: Amendment commits successfully
    Given an amendment holds the lock
    When the analyst re-runs (re-engaged) and produces the amended spec
    And the amendment proposes v(N+1) with the new arrow grid
    Then the component computes the set of affected arrows
    And signals all affected "running" passes to abort
    And writes the new grid atomically
    And records the v(N+1) commit log entry
    And releases the lock
    And the next queued amendment may proceed

  # ---- Dependency-check against in-flight passes ----

  @deferred
  Scenario: Affected pass aborts
    Given pass P1 on (implementer, contextA) is running
    And P1's arrow declared dependencies [{artifact: "features/contextA/payment.feature", granularity: section, on-change: invalidate}]
    When an amendment changes that file's section
    Then the dep-check identifies P1 as affected
    And signals P1's runner to abort with reason "invalidated"
    And the arrow's status becomes "invalidated"
    And P1's findings are preserved with grid-version tag vN

  Scenario: Unaffected pass continues
    Given pass P2 on (analyst, contextB) is running
    And P2's arrow declared no dependencies on the amended file
    When the amendment commits
    Then P2 continues running against vN
    And records its completion against vN (not v(N+1))

  @deferred
  Scenario: Conservative fallback for no-deps arrows
    Given pass P3 on (architect, contextB) is running
    And P3's arrow declared no dependencies at all
    When the amendment commits
    Then P3 is treated as affected (conservative fallback)
    And P3 is aborted with reason "invalidated"
    And operator-tooling can detect this and flag "P3 should have declared dependencies"

  # ---- Atomic write (D31: versioned files + grid.current pointer) ----

  Scenario: Successful atomic write of v(N+1) with fsync ordering
    Given the amendment is ready to write v(N+1)
    When the component writes the grid
    Then it writes content to ".ghyll/grid.v(N+1).yaml.tmp"
    And fsync's the temp file (content durable)
    And fsync's the containing directory (the new directory entry is durable)
    And ONLY THEN renames temp → ".ghyll/grid.v(N+1).yaml"
    And fsync's the directory again (the rename is durable)
    And ONLY THEN updates ".ghyll/grid.current" atomically
    And after the .current update, a fresh reader observing grid.current = "v(N+1)" is guaranteed to see grid.v(N+1).yaml intact (no torn read possible due to ordering above)

  Scenario: Crash mid-write
    Given the temp file is partially written
    When the process is killed
    Then on restart, the temp file is unlinked (cleanup)
    And the previous version remains current
    And the amendment is re-queued (or marked failed per operator policy)

  # ---- Concurrent amendments serialize ----

  Scenario: Both amendments queue and second lands against first's output
    Given amendment A1 (changes cross-context/A-B.md) and amendment A2 (changes cross-context/B-C.md) arrive at roughly the same time
    When the amendment component processes them
    Then both enter the queue
    And A1 commits first (FIFO), producing v(N+1)
    And A2 acquires the lock against state v(N+1)
    And A2 commits, producing v(N+2)
    And the audit log shows v(N+1) and v(N+2) as separate commits, not merged

  @deferred
  Scenario: Two amendments touch the same artifact
    Given A1 changes lines 1-20 of cross-context/A-B.md
    And A2 changes lines 15-30 of the same file
    When A1 commits and A2 reaches the lock
    Then A2 re-reads the current state of cross-context/A-B.md (post-A1)
    And the analyst that produced A2 must re-justify the amendment against the post-A1 state
    And if the post-A1 state already addresses A2's concern, A2 becomes a no-op
    And if A2's concern still applies, A2 commits as a separate v(N+2)

  # ---- Grid version visibility ----

  Scenario: All components read the current grid version
    Given the runner, state-machine engine, and operator UI all need to know the current grid version
    When any of them queries
    Then they read .ghyll/grid.current (or call a get-version API)
    And get the same answer regardless of which they query

  Scenario: Pass identity uses grid version
    Given a pass P5 is created on arrow A1
    And the current grid version is v(N+1)
    Then P5's arrow-id is computed as "(role-pair, stratum, context, v(N+1))"
    And if the same arrow is re-traversed after v(N+2), the new pass has a different arrow-id

  # ---- Adversarial additions: liveness and deadlock ----

  @deferred
  Scenario: Amendment lock held by a process that crashed
    Given an amendment is committing and the harness crashes mid-write with the grid write-lock still held
    When the harness restarts
    Then crash recovery detects the orphaned lock (lock file's owner PID is no longer alive)
    And releases the lock as part of recovery
    And the half-written grid.v(N+1).yaml.tmp is unlinked
    And the next amendment may proceed normally
    And no operator action is required to break the deadlock

  @deferred
  Scenario: Amendment waiting on attestation that is waiting on an aborted pass
    Given an amendment A1 is queued waiting for the lock
    And pass P1 is mid-attestation (clause C5 awaiting verdict)
    And pass P1 has been aborted with reason "invalidated" by a previous amendment
    When A1 acquires the lock
    Then A1 does NOT block on P1's pending attestation (P1 is aborted; its attestation requests are cancelled)
    And A1 commits in bounded time (default: same as a normal commit)
    And the cancelled attestation requests emit OperatorEvents ("attestation-cancelled-by-abort") so the operator UI clears them

  # ---- Adversarial additions: FIFO under contention ----

  Scenario: FIFO ordering under contention
    Given 5 amendments A1, A2, A3, A4, A5 arrive in that order over the span of 1 second
    When all are queued
    Then they commit in strict order A1 → A2 → A3 → A4 → A5
    And no amendment is reordered ahead of an earlier one due to scheduling
    And the commit log records all 5 commits with monotonically increasing grid-versions

  Scenario: Queue capacity refuses overflow
    # The queue's MaxLen cap is wired today
    # (runner.AmendmentQueue.Enqueue returns ErrAmendmentQueueFull).
    # The full OperatorEvent "amendment-queue-growing" publication +
    # R/C reporting integration is deferred surface; the capacity
    # refusal (the underlying signal) is verifiable now.
    Given the amendment queue is configured with capacity 3
    When 3 amendments are enqueued
    Then the queue is at capacity
    And a 4th enqueue is refused with ErrAmendmentQueueFull

  # ---- Adversarial additions: reader/writer race ----

  Scenario: Reader observes grid.current between updates
    Given the amendment component is updating from vN to v(N+1)
    And a reader process opens .ghyll/grid.current at the exact moment of the rename
    When the rename is atomic (POSIX rename)
    Then the reader sees either "vN" OR "v(N+1)" — never an empty file, never a torn write
    And the corresponding grid.v*.yaml file exists at whichever version the reader observed
    And the reader can proceed without retry / error handling
