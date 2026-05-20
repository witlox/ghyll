# Tier 2 Adversarial Pass (gate-2)

Three cold-context adversaries reviewed the Tier 2 (ADR-016 operator
session + attestation modal) implementation against the gate-1 disposition
log. Findings consolidated below; remediation dispositions tracked in
`validation-impl-pass-tier2-adversarial-remediation.md`.

## Counts

| Source       | Critical | High | Medium | Low | Test gaps |
|--------------|---------:|-----:|-------:|----:|----------:|
| Security     |        2 |    5 |      5 |   5 |         — |
| Concurrency  |        4 |    6 |      5 |   4 |         7 |
| Correctness  |        6 |   14 |      6 |   — |         — |
| **Total**    |   **12** | **25** | **16** | **9** |     **7** |

## Critical

### CORR-A-1 — Engine persistence drops every Tier 2 column on write
`engine/attestations.go:43-53, 229-239, 81-93, 106-114, 290-300` —
`insertAttestation` + `upsertAttestationInTx` INSERT only the 12 Tier 1
columns; the 7 columns added by `ensureUnitColumns` (pass_id, context,
stratum, adversary_role, unit, unit_payload_json, hint_json) are never
written. Mirror gap on the read side (`readAttestation`,
`readAttestationInTx`, `listAttestations`). Net effect: tree JSONL has
the full record; engine cache has a stripped copy. They disagree by
construction.

### CORR-A-2 — CatchUpAttestations emits spurious divergence events on every boot
`engine/attestations.go:177-282` — Downstream of A-1, conflict-probe
runs `AttestationRecordsEqual(rec, engineRow)` which compares all Tier 2
fields. Engine row always lacks them → comparison always returns false →
UPDATE fires + `OpEventAttestationAuditDurabilityFailed` published PER
RECORD on every session boot. Operators see fatal-shaped audit signals
for entirely-normal startup.

### CORR-A-3 — Tier 1 → Tier 2 upgrade boot is unrecoverable
`cmd/ghyll/session_engine.go:184-189` — A project upgrading from Tier 1
(flat JSONL only, no `attestations/` tree directory) hits
`LoadFromTree(treeRoot, attCount>0)` with `attCount>0` (engine has rows)
and tree absent. `LoadFromTree` returns `ErrAttestationAuditLost`; init
closes the store. Every subsequent verdict is dropped. No flat→tree
migration step exists.

### CORR-A-4 — `AttestationStore.Record` never calls `ValidateUnitPayload`
`runner/attestationstore.go:360-382` — Contracts doc + ADR-016 Part C
require `Record` to call `ValidateUnitPayload` before the primaryWriter.
Implementation only calls `validateAttestation`. The modal driver
(`cmd/ghyll/modal_driver.go:349, 406`) is the sole site that validates;
`/attest` CLI, recovery replay, future operator endpoints all bypass.
SEC-H-1 partially overlaps.

### CORR-A-5 — `AdversaryRole` has no production producer (F-3 un-remediated)
`runner/adversarial.go:75-330` holds the AdversaryRole on the Adversary
struct but never constructs an AttestationRecord. `buildRecord`
(modal_driver.go:472-491) + `/attest` (session.go:1480-1500) never set
it. The dispatcher's `OpEventAttestationRequested` carries no adversary
signal. The 3-role-chain branch of `EncodeAttestationPath` is dead code
in production; the non-deferred BDD scenario is wired via a hand-built
record only.

### CORR-A-6 — `validateAttestation` does not enforce PassID-empty rejection
`runner/attestationstore.go:481-538` — F-6 disposition relied on
`EncodeAttestationPath` rejecting; the only enforcement is inside the
tree writer's callback. If the flat writer is the primary (tests +
fallback) or `SetPrimaryWriter(nil)`, empty PassID writes successfully.
Engine row then has empty pass_id (column default).

### SEC-C-1 — Path traversal via operator-controlled Context
`runner/attestation_tree.go:391,407,414` — `sanitizePathSegment`
whitelists `[a-zA-Z0-9_.-]`. The two-char string `..` passes; `safeSegment("..")`
returns `".."` (≤255 bytes); `filepath.Join` normalizes the path,
cancelling `v<N>`. An arrow whose grid YAML carries `Context: ".."`
writes outside the `v<N>` subtree. Combined with crafted Stratum, the
attacker controls 2 of 5 components.

### SEC-C-2 — Symlink-targeted truncation
`runner/attestation_tree.go:161-187, 193-239` —
`TruncateTrailingPartialAll` walks the tree with `filepath.Walk` then
`os.OpenFile(path, O_RDWR, ...)` which follows symlinks. An attacker
placing `ln -s /home/user/.ssh/known_hosts attestations/v1/_/stratum-_/init/p-1.jsonl`
causes Ghyll to truncate arbitrary writable files at session start
when Recovery signals trailing-partial.

### CONC-C-1 — REPL + TermModal use independent `bufio.Scanner` instances
`cmd/ghyll/repl.go:17` + `cmd/ghyll/modal/modal.go:108,188` — Each
`PresentVerdict`/`PresentEscalation` builds a fresh `bufio.Scanner` over
`m.In`. `bufio.Scanner` reads in 4 KiB chunks. When the operator types
the modal answer plus a follow-up REPL line in quick succession, the
modal-scanner consumes the answer + buffers the remainder; the scanner
is dropped; the REPL scanner reads only new bytes. The buffered REPL
line vanishes.

### CONC-C-2 — `readLineCtx` leaks goroutines on ctx-cancel
`cmd/ghyll/modal/modal.go:243-266` — On ctx.Done, returns ctx.Err()
but the goroutine started at line 249 keeps blocking on
`scanner.Scan()` → stdin read. When the operator's next byte arrives,
the orphan wakes, reads the line, writes to a `done` channel nobody
reads — the line is consumed. Across cancellations the process
accumulates orphan readers racing for the next stdin byte.

### CONC-C-3 — `DrainPending` drops tail items on non-cancel error
`cmd/ghyll/modal_driver.go:271-310` — After
`snapshot := d.pending; d.pending = nil`, if `handleRequest` returns a
non-cancel error on item `r_k`, the function returns immediately.
Items `r_{k+1}…r_n` from the snapshot are dropped; their attestRefs
remain in `d.inFlight`. Subsequent re-publish with same ref is
dedup-suppressed → clauses never re-presented until session restart.

### CONC-C-4 — Cancel-path requeue drops snapshot tail
`cmd/ghyll/modal_driver.go:285-288` — On ctx-cancel, only `r_k` is
prepended back to `d.pending`. Items `r_{k+1}…r_n` lost (same
dangling-inFlight pattern). Gate-1 F-14 promised pending items survive
cancellation; violated for everything after the cancelled item.

## High

### CORR-A-7 — Init-path encoding spec doc stale vs BDD
Remediation log F-18 says role-pair/context/stratum all literal `"init"`;
BDD + code use `"init__<target>"` + `"_"` placeholders.

### CORR-A-8 — Recovery republish loses Context/Stratum
`engine/recovery.go:325-331` — Event payload carries only ArrowID,
PassID, ClauseID + Detail string. `EnqueueFromRecovery` re-derives
Context/Stratum from the LIVE grid via arrowResolver — wrong if grid
shifted post-amendment.

### CORR-A-10 — `residueNoteMaxBytes` racy + spec-incompliant
`cmd/ghyll/modal_driver.go:35` — Plain int on modalDriver; F-24
required `atomic.Int64` on `AttestationStore`. Read-during-DrainPending
can observe torn 8-byte value on 32-bit ARM.

### CORR-A-11 — `/attest` CLI silently bypasses unit-payload validation
`cmd/ghyll/session.go:1480-1500` — Constructs record with `Unit=""`.
`ValidateUnitPayload` special-cases empty Unit as legacy-OK. Operator
can `/attest att-X pass` without producing evidence.

### CORR-A-12 — `/op-id` rejection messages don't match BDD wire forms
`cmd/ghyll/session.go:1378-1402` — Returns plain English strings; BDD
scenario at attestation.feature:175-185 (NOT @deferred) requires
`op-id-invalid-characters` / `op-id-too-long` / `op-id-required`.

### CORR-A-13 — `OpEventAttestationRequested` carries no Context/Stratum/SourceRole/TargetRole/GridVersion
`runner/dispatcher.go:271-281` — Modal driver must re-derive via
arrowResolver against the LIVE grid; persisted record then disagrees
with its own GridVersion field if grid changed.

### CORR-A-14 — `recordReplay` PassID-empty branch is dead code in Tier 2
`runner/attestationstore.go:392-407` — All tree files carry
PassID-populated rows. Legacy flat row never reaches recordReplay in
Tier 2. Either rationale is wrong or there's a missing flat-file
fallback at boot (A-3 follow-on).

### CORR-A-15 — Modal driver `inFlight` not cleared on backpressure drop
`cmd/ghyll/modal_driver.go:196-207` — Backpressure event published but
no recovery signal makes the dropped request re-appear within-session.

### CORR-A-18 — Init AttestationRecord producer missing
EncodeAttestationPath's init branch fires when `AttestedByRole == "init"`.
No code in bootstrap/runner/cmd/ghyll constructs such a record. The
non-@deferred BDD scenario can't be wired without an init-side producer.

### CORR-A-19 — `safeSegment` truncation skips Reason annotation
`runner/attestation_tree.go:96-138` — Bus event published yes; F-17
contract also requires `rec.Reason` ← `"path-truncated:<segment>"`.
Reason mutation is missing.

### CORR-A-21 — `validateOpID` Unicode/RTL bypass
`cmd/ghyll/session.go:1378-1402` — Rejects leading `.`/`-` but
multibyte UTF-8 sequences pass byte-level checks. `alice‮.gpj`
(RTL override) accepted; renders as `aligpj.ecila` in UI (phishing).
SEC-L-1 overlaps.

### SEC-H-1 — `validateAttestation` skips Tier 2 enforcement (PassID, Unit, payload-cap)
Overlaps CORR-A-4 + A-6 + missing AdversaryRole-contains-`__` check
(verifier checks at line 178; Record does not).

### SEC-H-2 — Op-id never validated outside REPL `/op-id` slash
`cmd/ghyll/session.go:1378-1402` + `runner/attestationstore.go:481` —
Tampered JSONL line with `"op_id":"alice ../../bob"` or
`"op_id":"[2J[H"` loads cleanly. The loaded record flows
back to terminal via `/attestations` (`session.go:1543-1544`) with NO
`sanitizeOneLine`.

### SEC-H-3 — Aggregate divergence verifier silently passes when both surfaces empty
`runner/attestation_verifier.go:269-317` — `engineHasRows=false`
always; missing flat+tree returns clean even if engine has rows.
Attacker can `rm -rf .ghyll/attestations*` pre-session-start to
suppress evidence.

### SEC-H-4 — Tree-walker accepts JSONL records under arbitrary paths
`runner/attestationstore.go:646-685, 690-730` — `LoadFromTree` does
NOT verify that file's path matches `EncodeAttestationPath(rec)`.
Combined with SEC-C-1, attacker can hide records under misleading
hierarchy.

### SEC-H-5 — Hint/Concept rendered verbatim to operator TTY
`cmd/ghyll/modal/modal.go:111-120, 191-201` — Hint fields from grid +
dispatcher hit `m.Out` with no sanitization. Clause concept with ANSI
escape (`[2J[H]0;PWNED\x07`) sets terminal title /
clears screen / smuggles OSC sequences.

### CONC-H-1 — `InsufficientBasisTracker.Record` publishes under `t.mu`
`runner/insufficient_basis_tracker.go:67-99` — Subscriber that calls
back into the tracker deadlocks (sync.Mutex non-reentrant). Today
survives by coincidence.

### CONC-H-2 — `AttestationTreeWriter.recordTreeFailure` publishes under `w.mu`
`runner/attestation_tree.go:263-275` — Same publish-under-lock; flat
JSONL writer at attestation_jsonl.go:161-173 too. Subscriber touching
AttestationStore hangs.

### CONC-H-3 — `AttestationStore.Record` fires observers under write lock
`runner/attestationstore.go:344-348, 360-382` — `emit` runs inside
held write lock. IB-tracker observer calls Record which publishes; held
locks: AttestationStore.mu(W) + ibTracker.mu + modalDriver.mu.

### CONC-H-4 — `OperatorBus` docstring contradicts implementation; no Unsubscribe
`runner/operatorbus.go:18-21` claims "publishers hold mutex during
fanout" — Publish actually snapshots-then-releases. Bus has no
Unsubscribe → modal driver outlives `closeEngine` because bus holds a
strong reference.

### CONC-H-5 — `OpEventEscalationPresented` double-publishes on retry
`cmd/ghyll/modal_driver.go:375-383` — Published BEFORE PresentEscalation;
on ctx-cancel the request requeues, next drain re-publishes "presented"
without a paired "resolved". One-resolved-per-presented invariant broken.

### CONC-H-6 — Escalation refs with gridVersion=0 when arrow missing
`cmd/ghyll/modal_driver.go:168-191` — `arrowResolver` returns false →
`gridVer=0` → synthesized attRef differs from dispatcher's. Dedup fails;
both verdict + escalation modals queue; two records per resolution.

## Medium

### SEC-M-1 — Residue cap enforced after collection
Modal `bufio.Scanner.Buffer(_, 1024*1024)` lets one line be 1 MiB.
Operator residue trimmed + handed off; cap-reject runs after collection.

### SEC-M-2 — `extractAttRef` doesn't sanitize on newline
Detail format uses space/tab separator; `IndexAny(rest, " \t")` misses
`\n` → newline-containing ref smuggles into dedup key.

### SEC-M-3 — `Reason` field unbounded, never sanitized in Record
`/attest <ref> <verdict> [reason]` reads reason as the trailing slash
command bytes. No length cap, no control-byte strip.

### SEC-M-4 — JSONL Observer error path silent without bus
Observer path increments writeErrors but only publishes if bus != nil.
Tier-1-style call without WithBus loses failure signal.

### SEC-M-5 — `recordTreeFailure` Detail leaks error chain
`err.Error()` chain includes absPath which may carry attacker-crafted
Context/Stratum. Some printer paths skip sanitizeOneLine.

### CONC-M-1 — Modal driver Record-fail path skips OpEventClauseFailVerdict
`cmd/ghyll/modal_driver.go:332-373` — On `store.Record` error, the
producer-fix signal never fires; generic error shown.

### CONC-M-2 — `modalDriver.OnEvent` synchronous on publisher's goroutine
Backpressure publish at appendRequest is itself a publish-from-subscriber.
RWMutex spec: recursive RLock blocks behind pending writer — publisher
hangs if Subscribe concurrent.

### CONC-M-3 — `Session.Close` doesn't wait for DrainPending to unwind
`cmd/ghyll/session.go:420-442` — `closeEngine` proceeds while
DrainPending mid-modal; tree writer can be closed before in-flight
Record completes → write-after-close.

### CONC-M-4 — Signal handler `os.Exit` skips sessionCancel
`cmd/ghyll/repl.go:21-36` — SIGINT/SIGTERM: checkpoint + os.Exit(0).
No sess.Close() call. In-flight goroutines terminated mid-write.

### CONC-M-5 — `ibTracker.Reset` runs after escalation publish
`cmd/ghyll/modal_driver.go:416-431` — Subscriber observing
OpEventEscalationResolved sees not-crossed (correct); but reset
ordering relative to Record is brittle.

### CORR-A-22 — `LoadFromTree` lexical order may reverse pass-causal order
`runner/attestationstore.go:646-685` — Two passes sharing a clause:
pass-id sort may not match timestamp order; recordReplay errors on
ID-conflict with different content → entire walk aborts.

### CORR-A-23 — `bootstrap.Read` error path leaves IB rounds at 0
`cmd/ghyll/session.go:293-300` — On bootstrap.Read failure
(non-init project), values stay 0. `NewInsufficientBasisTracker(0, ...)`
disables escalation entirely without diagnostic.

### CORR-A-24 — `ensureUnitColumns` existence check outside transaction
`engine/store.go:200-203` — `attestationColumns()` runs before
`tx.Begin()`. Fragile if shared writer ever supported.

### CORR-A-25 — `AttestationRecordsEqual` slice comparison order-sensitive
`runner/attestationstore.go:146-153` — Inspected list compared
positionally; comment claims deterministic order but no enforcement.

### CORR-A-26 — Recovery `OpEventRecoveryAttestationReplay` not filtered with doc
`engine/recovery.go:325-331, 421-425` — Modal filters on
republished only; replay events drop. Behavior correct, documentation
gap.

### CORR-A-27 — Engine schema lacks `pass_id != ''` CHECK
Default empty string; no schema-level enforcement. Application layer
required (A-6 fix).

## Low

### SEC-L-1 — `validateOpID` Unicode/BiDi normalization
Multibyte UTF-8 sequences pass byte-level checks; ZWSP / RTL override
allowed. Overlaps CORR-A-21.

### SEC-L-2 — `validateOpID` allows trailing dot
Only first byte checked. `alice.` accepted; trailing dots stripped on
Windows-style FS.

### SEC-L-3 — `Unit == ""` skips both verifier + ValidateUnitPayload
Tampered JSONL with empty Unit + 64 MiB Residue loads in-memory unchecked.

### SEC-L-4 — `safeSegment` SHA-256 prefix 64 bits → birthday-feasible
Two over-cap segments collide at ~2³² records.

### SEC-L-5 — `loadOneTreeFile` single-line truncation edge
Brand-new file from crash mid-write becomes 100% truncated.

### CONC-L-1 — `OperatorBus.WithClock` mutates `b.now` without locking
Race-detector flag in tests setting clock after subscribers started.

### CONC-L-2 — `DrainPending` cap-exceeded publish is publisher-from-subscriber
Filtered by kind-switch today; future enqueue of backpressure events
would re-enter.

### CONC-L-3 — `enqueueVerdict` Detail JSON parsed without size cap
Megabyte JSON pegs CPU on publish goroutine (synchronous subscriber).

### CONC-L-4 — `extractAttRef` returns trailing garbage
No `strings.IndexAny` match returns entire rest including `\n`.

## Test gaps

| ID | Description |
|---|---|
| T-1 | No race-detector coverage for concurrent Publish + DrainPending |
| T-2 | TermModal ctx-cancel doesn't assert goroutine exits |
| T-3 | No test feeds single io.Reader to REPL + TermModal (CONC-C-1 uncovered) |
| T-4 | No test for DrainPending error-mid-snapshot dropping tail (CONC-C-3/C-4) |
| T-5 | No test where `arrowResolver` returns false for escalation (CONC-H-6) |
| T-6 | No subscriber-calls-tracker test under `-race -timeout 5s` (CONC-H-1) |
| T-7 | No teardown-ordering test where Close runs while DrainPending mid-modal |

## Gate-1 status (correctness adversary verified)

7 critical-or-high gate-1 items un- or partially-remediated:
- F-1 (LoadFromTree replaces LoadFromJSONL) — PARTIAL (CORR-A-3)
- F-2 (Pure encoder + dispatcher stamps) — PARTIAL (CORR-A-13)
- F-3 (AdversaryRole producer) — NO (CORR-A-5)
- F-6 (PassID empty rejection) — PARTIAL (CORR-A-6)
- F-9 (ValidateUnitPayload in Record) — NO (CORR-A-4)
- F-13 (ValidateOpID wire forms) — PARTIAL (CORR-A-12 + A-21)
- F-17 (Path truncation Reason annotation) — PARTIAL (CORR-A-19)
- F-24 (atomic.Int64 cap) — NO (CORR-A-10)

Recommended remediation order (per no-deferral policy):
1. CORR-A-1/A-2 — engine persistence parity (foundational)
2. CORR-A-3 — Tier 1 → Tier 2 flat→tree migration
3. SEC-C-1 — path traversal guard (`.` / `..` rejection in safeSegment)
4. SEC-C-2 — symlink-no-follow on tree truncation
5. CORR-A-4 + A-6 + SEC-H-1 — validation in Record (PassID, Unit, payload)
6. CONC-C-1 + C-2 — stdin scanner consolidation
7. CONC-C-3 + C-4 — DrainPending snapshot-tail integrity
8. CORR-A-5 — AdversaryRole producer
9. CORR-A-12 — wire-form errors for `/op-id`
10. CORR-A-13 — extend OpEventAttestationRequested payload
11. SEC-H-2..H-5 — op-id replay + ANSI sanitization
12. CONC-H-1..H-6 — publish-outside-lock + escalation invariants
13. Medium + Low + test gaps
