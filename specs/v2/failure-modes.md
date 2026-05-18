# Failure modes

How v2 ghyll fails — grouped by topic, each with blast radius and
intended degradation. Consolidated from the per-component FM tables
in `specs/direction/components/*.md`.

Each failure mode has:

- **ID** — `FM-NN`, stable across the project.
- **Source** — the component spec that originally identified it.
- **Severity** — `info | low | medium | high | critical`. Uses the
  same enum as findings (`gates.md` §7.3). The threshold for "this
  must be handled before code ships" is `high` and above.
- **Blast radius** — what is affected when this fails.
- **Intended degradation** — how the harness should behave when it
  fails. Not what the failure is; what we *do* about it.

The schema's contracts and the operator event bus (D43) provide
visibility into most of these. The intended-degradation column
distinguishes "fail loud and visibly" from "degrade gracefully with
operator hint."

---

## Process / evaluator failures

These cluster around external evaluators (compile, lint, mutation
runs) and the harness itself. The runner component owns most of
them, but adversarial and init components hit similar issues when
they spawn subprocesses.

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-01 | Evaluator binary crashes mid-evaluation | runner | medium | Single clause | Record `result: {pass: false, details: {crash: <stderr/stdout>}}`; clause status `fail`. Operator triages: real failure vs. evaluator bug. |
| FM-02 | Evaluator hangs (exceeds timeout) | runner | medium | Single clause + pass duration | Kill evaluator after timeout; clause status `unevaluated` with reason `evaluator-timeout`. Recurring timeouts are a binding bug. |
| FM-03 | Evaluator returns malformed JSON | runner | medium | Single clause | Treat as FM-01 (crash). Raw output preserved (≤16KB) in evaluation-run for forensic. The runner is strict about evaluator-output shape. |
| FM-04 | Evaluator killed by OS OOM | runner | high | Single clause | Distinguished from `evaluator-timeout`. Status `fail` with `details.error = "evaluator-killed: oom"`. NOT silently passed. |
| FM-05 | Evaluator returns oversized output | runner | medium | Single clause | Enforce `max-output-bytes` (default 16MB); exceeding fails with `details.error = "evaluator-output-oversized"`; evaluator process killed at limit trip. |
| FM-06 | Evaluator leaves zombie children | runner | medium | Per-pass throughput | Runner reaps remaining children in evaluator's process group within 5s. No zombie accumulation across passes. |
| FM-07 | Evaluators competing for CPU / IO on a single machine | runner | low | Pass throughput | Runner does not orchestrate scheduling; relies on OS. Future component (scheduler) may add per-evaluator-class concurrency limits. |
| FM-08 | Producer role doesn't respond to hint request | runner / attestation | medium | Single pass | After timeout (default 1h, init-override), clause is `unevaluated` with reason `producer-no-response`. Operator can re-route. |
| FM-09 | Runner crashes mid-pass | runner | high | Single pass | On restart, the runner detects the orphaned pass (no `completed-at`, no abort record), marks it `aborted` with `reason: crash`, continues. User must re-traverse. |

## I/O and persistence failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-10 | Grid file write fails (disk full, permissions) | init / amendment | high | Project | Exit with `grid-write-failed`; no partial state on disk; project remains at the previous grid version. |
| FM-11 | Amendment commit fails partway (disk full) | amendment | high | Single amendment | Roll back: unlink temp file, leave previous version current, re-queue amendment (or fail with operator alert per retry policy). |
| FM-12 | Crash mid-write of versioned grid | amendment / init | high | Single amendment | Temp file unlinked on restart; previous version stays current; amendment re-queued or marked failed. |
| FM-13 | `grid.current` points to missing version | state-machine / init | critical | Project | Engine refuses to start; surfaces `grid-current-points-to-missing-version`; operator must restore the file, re-point `grid.current`, or re-init. |
| FM-14 | `grid.current` corrupted (binary, empty, malformed) | init | critical | Project | Engine refuses with `grid-current-malformed`; reports actual content; operator must repair or remove (forcing re-init). |
| FM-15 | Multiple grid versions on disk, `grid.current` absent | init | high | Project | Engine does NOT silently pick latest. Surfaces `grid-current-absent` with available versions; operator declares which is current. |
| FM-16 | Checkpoint write fails (disk full) | state-machine | high | Single pass | Pass finalization fails; pass remains `running` in memory until checkpoint write succeeds. Repeated failures escalate to operator. |
| FM-17 | Checkpoint log truncated mid-write | state-machine | high | Most recent pass | Crash recovery detects truncation via Merkle hash mismatch; rolls back to last verified record; the affected pass is re-marked `aborted: crash`. |
| FM-18 | In-memory state diverges from checkpoint log | state-machine | high | Whole project | Checkpoint log is source of truth. On detected divergence: alert; engine re-reads from log. May require operator intervention. |
| FM-19 | Concurrent writes to same checkpoint file | state-machine | medium | Checkpoint log | Runner serializes checkpoint writes through project-wide write-lock. Two concurrent finalizations queue FIFO. |
| FM-20 | Attestation record write fails (disk) | attestation | high | Single verdict | Verdict capture fails; operator prompted to retry. Clause stays in prior status; no partial-state hazard. |
| FM-21 | Attestation record hand-edited to falsify history | attestation | low | Audit integrity | Attestation file isn't tamper-proof against malicious operators. Schema relies on append-only convention. ed25519-signing per record is a future enhancement. |

## Concurrency failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-22 | Two passes on same `(role, context)` requested | runner | medium | Two passes | Runner refuses second pass with `single-active-role-violation` at pre-spawn. Operator must wait or abort first. |
| FM-23 | Two adversarial phases on same arrow concurrent | adversarial | medium | Two passes | Forbidden by `single-active-role-instance` per-(role, context) lock. Caught by runner before spawn. |
| FM-24 | Two amendment-component instances running concurrently | amendment | high | Project | Should be impossible (single process per project; A-4 of init.md). If it happens: OS-level file lock on temp prevents simultaneous writes; second fails with `lock-conflict`. |
| FM-25 | Pass that should have been aborted misses the signal | amendment | medium | Single pass | Pass continues against vN. On completion, state-machine detects grid-version mismatch and treats result as `invalidated`. Pass's findings preserved with original vN tag. |
| FM-26 | Concurrent updates to same clause from concurrent callers | state-machine | low | Single clause | Per-clause lock in runner; state-machine assumes serialized updates per clause. If concurrent updates somehow reach the engine, later timestamp wins, warns. |
| FM-27 | Amendment lock held by a process that crashed | amendment | high | Project | Crash recovery detects orphaned lock (owner PID dead); releases as part of recovery; half-written temp unlinked; next amendment proceeds normally. No operator action required. |
| FM-28 | Amendment queue grows unboundedly | amendment | medium | Project throughput | Operator-tooling alerts on queue length above configurable threshold (default 10). `OperatorEvent: amendment-queue-growing` published. |

## State-machine integrity failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-29 | Engine receives transition for unknown clause | state-machine | low | Single transition | Reject with `unknown-clause-id`. Caller (typically runner) must register the pass and clause set first. |
| FM-30 | Engine receives illegal transition (e.g., `pass → pending`) | state-machine | medium | Single transition | Reject with `illegal-transition`. Current state preserved. The illegal-transition matrix is enumerated in tests. |
| FM-31 | Crash while clause is awaiting-attestation | state-machine | high | Single clause | Recovery does NOT mark the pass aborted; attestation request re-published on event bus on restart. Clause remains `awaiting-attestation`. |
| FM-32 | Crash between attestation write and clause-status flip | state-machine / attestation | critical | Single clause + audit | Recovery reads latest attestation record, reconciles clause-status to match (`pass`/`fail`/etc.). Recorded as recovery event for audit. No split-brain persists. |
| FM-33 | Project-status query concurrent with in-flight transition | state-machine | low | Single query | Read-lock invariant: query waits or sees consistent snapshot. |

## Adversarial-phase failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-34 | Adversary model returns malformed output (e.g., findings without required severity) | adversarial | medium | Single round | Treat as evaluator-output bug. Orchestrator retries the round with explicit shape reminder. Persistent → operator triage. |
| FM-35 | Adversary tier unavailable (depth-sensitivity needs tier not in routing config) | adversarial | high | Phase | Findings status `unevaluated` with reason `depth-below-required`. Operator escalates. Schema NEVER silently elevates to a deeper-than-required tier without that tier being declared. |
| FM-36 | Producer never responds to a finding (timeout) | adversarial | medium | Single finding | After timeout (default 1h, init-override), orchestrator marks round as stalled. Operator-routable. |
| FM-37 | Producer signals "fixed" but artifact is unchanged (loop bomb) | adversarial | high | Single finding | Orchestrator detects no-op via content-hash compare (pre/post). Refuses to spawn next adversary. Emits `OperatorEvent: producer-signal-without-change`. Round counter does NOT advance. |
| FM-38 | Adversary attacks non-deterministic upstream (variable findings per round) | adversarial | low | Single arrow | Schema does not assume determinism. Variability between rounds is legitimate. If it causes non-convergence: escalation per FM-39. |
| FM-39 | Remediation does not converge in `remediation-rounds-max` rounds | adversarial | high | Single arrow | Orchestrator stops loop; escalates with kind `remediation-non-convergence`. Operator: accept-risk OR route artifact for deeper rework upstream. |
| FM-40 | Adversary clean-context spawn leaks state | adversarial | high | Phase integrity | Binding bug in adversary spawning. Orchestrator must validate clean context per spawn (new session token, no prior message history). Implementation requirement; not silently tolerated. |

## Operator interaction failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-41 | Operator abandons init mid-flow | init | medium | Project init | No grid recorded; next invocation re-runs init from scratch. Partial attestations preserved in `attestations/` for forensic value but do not count. |
| FM-42 | Operator declares conflicting verdicts (two confirms with different modifications) | init / attestation | low | Init session | Last verdict per clause wins; attestation log records both for audit. Operator-tooling flags the conflict post-init. |
| FM-43 | A binding command is not installed | init | high | Init session | Init fails the binding's self-test; presents operator with a clear error pointing at missing tool; does NOT auto-install. Operator installs / declares alternative. |
| FM-44 | Auto-propose response from operator cannot be parsed | init | low | Single clause | Re-prompt with parse-error; clause's verdict slot is not consumed. |
| FM-45 | Bounded-context name collides with existing one | init | medium | Init session | Refuse with `context-name-collision`; operator renames or merges. |
| FM-46 | Operator submits verdict without active session | attestation | low | Single verdict | Refused with `no-active-session`. Must declare op-id first. |
| FM-47 | Two operators submit verdicts on same clause concurrently | attestation | low | Single clause | Per-clause serialization; later verdict wins; both recorded chronologically. Audit log shows conflict. |
| FM-48 | Operator session times out mid-attestation | attestation | low | Single verdict | Pending request preserved; next session re-presented. Round counter NOT incremented (timeout isn't `insufficient-basis`). |
| FM-49 | Producer attempts self-attestation (schema violation) | attestation | critical | Audit integrity | Verifier component detects (op-id matches a known producer's namespace, not operator identity); flags. Schema invariant is structural; implementation must enforce. |
| FM-50 | Operator declares spoofed op-id (different sessions claim same op-id from different machines) | attestation | low | Multi-operator project | Out of scope for v1. Schema records op-id as declared; trust is operator-side. ed25519 per-record is future. |
| FM-51 | Op-id contains path-traversal or control characters | attestation / init | high | Filesystem | Op-id validated at session start: refused if it contains `/`, `..`, NUL, newline, unicode RTL override, or shell metacharacters. Op-id NEVER reaches a filesystem path component (recorded in JSON only). |
| FM-52 | Oversized residue note submitted | attestation | low | Single record | Refuse with `residue-note-too-long` (default 16KB threshold). Re-prompt. No oversized record appended. |

## Schema / contract failures

| ID | Failure | Source | Severity | Blast radius | Intended degradation |
|---|---|---|---|---|---|
| FM-53 | Amendment proposes inconsistent grid (e.g., arrow outside the diamond) | amendment | high | Single amendment | Component validates against schema invariants (diamond shape, stratum vocabulary, etc.). Invalid proposals rejected; analyst that produced amendment must re-engage. |
| FM-54 | Crash recovery finds half-committed amendment | amendment | high | Project | On restart, recovery checks `grid.current` against on-disk files. Disagreement → alert; require operator decision. |
| FM-55 | Transition-refusal error suppressed or ignored by buggy caller | runner | medium | Whole project | Caller bug, not runner bug. Schema invariant is "runner refuses"; what callers do with refusal is outside scope. UI/tooling must surface refusals visibly. |
| FM-56 | Init refuses (low-risk profile) and operator overrides | init | info | Project | Override is allowed but requires a residue note recording why. The override is recorded in attestation log. |
| FM-57 | Operator escalates `insufficient-basis` to "requires-deeper-artifact" | attestation | medium | Single arrow | Pass is aborted with that reason; producer role re-routed at deeper tier; arrow re-traverses. Distinct from `invalidated` abort. |

---

## Failure modes the harness does NOT prevent

Stated explicitly per `gates.md` §13:

- **Shallow artifacts.** The schema makes "was the check run and did
  it pass" structural. It cannot make a thin spec deep or a shallow
  test meaningful. `depth-sensitive` clauses + adversarial-phase
  depth-classification + mutation-score are the *defenses*; they are
  not proofs.
- **Operator fatigue.** `attested` clauses depend on operator
  attention. Weight (§10.1) allocates attention; it does not create
  it. A rigid harness that demands many attestations trains
  rubber-stamping.
- **Definition-phase quality.** The grid is only as good as init.
  Residue and on-the-spot interruption counts are the signals of a
  weak init; they do not prevent one.
- **Cross-machine coordination.** Single-process per project is
  assumed (A-4 of `init.md`). Distributed ghyll is out of scope for
  v1; the locking model would need to become distributed.
- **Truly malicious operators.** Append-only attestation logs and
  declared op-ids deter mistakes but do not prevent a malicious
  operator with shell access from rewriting history. Crypto-signing
  per record is a future enhancement.

These are human-guarded responsibilities, made visible rather than
eliminated.

---

## Severity-by-count summary

| Severity | Count |
|---|---|
| critical | 4 |
| high | 17 |
| medium | 19 |
| low | 16 |
| info | 1 |
| **Total** | **57** |

The 4 critical FMs (FM-13, FM-14, FM-32, FM-49) cluster around:
- **Grid pointer integrity** (FM-13, FM-14) — without a valid
  `grid.current` the project cannot operate; engine refuses rather
  than guesses.
- **Attestation/status split-brain** (FM-32) — the most dangerous
  consistency hazard; recovery reconciliation is the defense.
- **Producer-self-attestation** (FM-49) — schema invariant; if
  violated, the entire trust model collapses; verifier component
  detects.

All 4 must be handled with explicit code paths before v2 ships.
The 17 high FMs need explicit handling. The 19 medium FMs need
defined behavior but can use a more uniform error path. The 16 low
+ 1 info can be handled informationally.
