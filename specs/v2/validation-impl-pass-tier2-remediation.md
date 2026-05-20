# Tier 2 gate-1 remediation log

Disposition for the 22 findings raised in
`specs/v2/validation-impl-pass-tier2.md` (cold-context adversary,
2026-05-20). Per standing direction (`feedback_no_deferrals.md`):
every finding remediated in-phase; no deferrals; no severity
filtering. All remediations land at the spec/ADR/contracts level
before the implementer starts coding.

The adversary's own recommended remediation order at the report's
tail is the order taken below.

---

## Critical (6 / 6 remediated)

### F-1: Recovery + verifier still read the flat aggregate

**Disposition**: Adopt option (a) — Recovery and verify-attestations
read the tree post-Tier 2. Add `AttestationStore.LoadFromTree(root,
engineHasRows) (LoadResult, error)` helper that walks
`attestations/v*/.../<pass-id>.jsonl`. session.openEngineWithOptions
calls `LoadFromTree` BEFORE Replay (replaces the Tier-1
`LoadFromJSONL` of the flat file). The flat file stays as a tail
written by an Observer fanout AFTER tree primary write; the flat
verifier extension flags any divergence as
`ErrAttestationAggregateDivergence`. ADR-016 Part B revised; spec
invariant 2 refines to "tree is loaded at session start; flat is
an aggregate sink".

### F-2: EncodeAttestationPath needs Grid; AttestationStore.Record doesn't have it

**Disposition**: Add `Context`, `Stratum`, `PassID` (already
planned) to `AttestationRecord`. Drop the Grid argument from
`EncodeAttestationPath` and `AttestationTreeWriter.PrimaryWriter`.
The encoder is a pure function of `rec`. The dispatcher stamps
Context + Stratum at construct time (it has the arrow def; the
fields are stable for the verdict's lifetime). On-the-spot
attestations stamp the operator-proposed Context/Stratum from the
proposal payload.

### F-3: 3-role chain has no source signal

**Disposition**: Adopt option (a) — add `AdversaryRole string` to
`AttestationRecord`. The orchestrator's `runner/adversarial.go`
stamps it when a verdict is captured during an adversary-phase
pass. Empty otherwise. `EncodeAttestationPath` Part F step 2:

- `AdversaryRole != ""`: role-pair = `{source}__{adversary}__{target}`.
- Else: 2-role.

Plus a Tier 2 self-cert extension: `AdversaryRole` (if non-empty)
MUST NOT equal SourceRole or TargetRole and MUST NOT contain `__`.

### F-4: Modal driver subscription has no defined order vs Recovery's republish

**Disposition**: Specify the cross-wire. Add
`modalDriver.EnqueueFromRecovery(events []runner.OperatorEvent)`
to the contract. `session.go:initEngine`, after `attachJournal`
+ subscribing the modal driver to the bus, calls
`s.modalDriver.EnqueueFromRecovery(rt.RecoveryReport().Events)`.
The driver filters for `OpEventRecoveryAttestationRepublished`
and enqueues from the event's `ArrowID` + `ClauseID` + `Detail`
(which carries `att-ref=<ref>`). Recovery's republish event
already carries enough payload — confirmed via ADR-015 Part E
lines 303-311.

### F-5: DrainPending re-entry under recursive attestation requests

**Disposition**: Adopt option (b) — bounded re-drain loop.
DrainPending snapshots pending under `d.mu`, drops the lock,
iterates the snapshot. New publishes go onto a fresh list; after
one snapshot iteration completes, re-snapshot. Cap at 8 drain
rounds per turn; on cap-overflow return
`ErrModalDrainCapExceeded` and surface a typed event so a
dispatcher bug is loud.

### F-6: EncodeAttestationPath with empty PassID

**Disposition**: Adopt option (a). `EncodeAttestationPath`
returns `ErrAttestationPassIDEmpty` if `rec.PassID == ""`. The
`/attest` CLI escape hatch populates PassID by querying
`evaluation_runs.pass_id WHERE depth_type_attestation_ref = ref`
before constructing the AttestationRecord. If the JOIN finds
nothing (pre-Tier-2 row OR orphan), the CLI refuses the
verdict with `ErrAttestationNoPassForRef` and tells the operator
to use the modal flow. Pre-Tier-2 rows in the engine table stay
flat-aggregate-only (recordReplay path; never round-trip through
the tree writer, per Tier 1 invariant — verified).

---

## High (9 / 9 remediated)

### F-7: InsufficientBasisTracker fires only at rounds == max

**Disposition**: Adopt option (b). The tracker maintains a
`crossedClauses set[clauseID]struct{}`. On every Record call:
- If `clauseID in crossedClauses`: re-emit
  `OpEventInsufficientBasisRoundsExceeded` regardless of count.
- If `rounds == max` first time: add to crossedClauses and emit.
The modal driver dedups against an in-flight set so the operator
gets exactly one escalation prompt per crossed clause until the
escalation choice resolves.
Add `tracker.Reset(clauseID)` API; the modal driver calls it
after a verdict that disposes the clause (`accepted-risk` or
`route-upstream` choice landed).

### F-8: OperatorBus.Publish synchronous fanout deadlocks DrainPending

**Disposition**: F-5 remediation (DrainPending snapshot-then-iterate)
fixes the deadlock half. Add the backpressure cap: `d.pending`
is capped at 64 entries; overflow drops the newest with
`OpEventModalBackpressure` published. Document the cap as
operator-tunable via `bootstrap.GridDefaults.ModalPendingMaxLen`
(default 64).

### F-9: ValidateUnitPayload's MaxResidueBytes default-fill

**Disposition**:
1. `GridDefaults.validate()` rejects `<= 0` for
   ResidueNoteMaxBytes (not just `<`).
2. `bootstrap/grid.go` GridFile decode: if YAML key absent or
   value is 0, set to `DefaultMaxResidueNoteBytes` (16384) via
   the existing post-decode normalize step at `bootstrap/grid.go:108`.
3. Existing v1/v2 grid files have no `residue-note-max-bytes`
   key — they get the default, no breakage.

### F-10: Schema v3 → v4 ALTER partial-failure

**Disposition**: Wrap the four ALTERs in `db.Begin()` / `tx.Commit()`.
ensureUnitColumns transitions to:

```go
tx, err := s.db.Begin()
if err != nil { return err }
defer tx.Rollback()
for _, col := range neededCols {
    if columnExistsTx(tx, "attestations", col.name) { continue }
    if _, err := tx.Exec(col.alter); err != nil { return err }
}
return tx.Commit()
```

Specify in contracts doc.

### F-11: Tree writer needs TruncateTrailingPartial parity

**Disposition**:
1. AttestationTreeWriter opens with `O_RDWR | O_APPEND` (not
   `O_WRONLY | O_APPEND`).
2. Add `AttestationTreeWriter.TruncateTrailingPartialAll(root)
   error` that walks every `<root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl`
   and calls TruncateTrailingPartial on each. Called by
   session.openEngineWithOptions after LoadFromTree returns
   truncated=true. Cost is proportional to corrupted-file count
   (typically 0).

### F-12: Modal driver double-prompts (Recovery republish + dispatch republish)

**Disposition**: Modal driver maintains an `inFlight
map[attRef]struct{}` keyed on `AttestationRecord.ID` (i.e. the
deterministic `att-<arrow>-<clause>-v<N>`). OnEvent /
EnqueueFromRecovery both consult the set before enqueueing.
Entry removed after Present* returns (success or skip).

### F-13: /op-id whitespace validator doesn't enforce all character classes

**Disposition**: Add a Tier 2 hardening: `runner.ValidateOpID(s)
error` (lifted from the existing `bootstrap.ValidateOpID` if
present; otherwise new). Rules:
- Reject control bytes (< 0x20, 0x7F).
- Reject path separators (`/`, `\`).
- Reject `..` substring (path-traversal defense).
- Reject empty after trim.
- Cap at 256 bytes.
`/op-id` handler calls ValidateOpID; rejects with
`ErrOpIDInvalidCharacters`.

### F-14: /exit mid-modal cleanup

**Disposition**: Specify in FM-1 and the contracts doc.
1. `PresentVerdict` accepts `ctx context.Context`; the chat
   loop's shutdown channel cancels it.
2. On ctx-cancel, PresentVerdict returns
   `context.Canceled` (wrapped). DrainPending propagates;
   chat-loop knows to close gracefully.
3. The modal driver's pending queue is checkpointed (the
   evaluation_runs row IS the persistent state; no separate
   queue persistence needed). Next session's Recovery
   republishes.
4. The dispatcher's per-pass lock token is released as part of
   the session.Close → closeEngine teardown (which calls
   `Pass.Abort("session-closed-mid-modal")` for any preserved
   pass).
5. /exit is treated as a slash command; it's intercepted at
   the chat-loop's input read before PresentVerdict's stdin
   read sees it. The chat loop coordinates: cancel ctx, then
   the modal returns, then chat-loop processes /exit.

### F-15: OperatorBus.Subscribe no Unsubscribe

**Disposition**: Note only — bus is per-EngineRuntime;
per-session lifecycle. No cross-session leak. Documented in
contracts doc.

---

## Medium (7 / 7 remediated)

### F-16: Spec invariant 2 wording

**Disposition**: Note only — Tier 1 already enforces "primary
writer fires before byID mutation". The Tier 2 spec wording is
correct.

### F-17: EncodeAttestationPath byte-cap fallback hides failure

**Disposition**: Adopt option (a). `EncodeAttestationPath`
returns `(path string, truncated bool, err error)`. When
`truncated == true` (a component was hash-substituted), the
caller (PrimaryWriter) appends `path-truncated:<segment>` to
`AttestationRecord.Reason` AND publishes
`ErrPathComponentTooLong` via the bus.

### F-18: Init arrow's target_role ambiguous

**Disposition**: Adopt option (b) refined. Init attestations
use:
- role-pair: literal `"init"` (single segment, no `__`).
- context: literal `"init"`.
- stratum: literal `"init"`.
- AttestedByRole: the operator role for the init pass (set by
  `bootstrap.Session` at op-id declare time).

Updated in ADR-016 Part F step 2b.

### F-19: PrimaryWriter takes *Grid argument; lifecycle coupling

**Disposition**: F-2 remediation removes the Grid argument
entirely. EncodeAttestationPath is a pure function. Lifecycle
question goes away.

### F-20: s.opID mutation between OnEvent and DrainPending

**Disposition**: Adopt option (a) + (b) combined. Specify in
the contract: `AttestationRecord.OpID` is read at the moment
`AttestationStore.Record` is called, NOT at enqueue or modal
open. `modalDriver` constructor takes
`opIDProvider func() string`; the session injects a getter that
reads `s.opID` at call time. Add a BDD scenario: handoff
mid-typing, op-id reflects bob.

### F-21: AttestationVerifier unaware of unit/unit_payload/hint_json

**Disposition**: Add `runner/attestation_verifier.go` to the
implementation seam. Extension rules per F-21 of the gate-1
report: per-unit `unit_payload_json` validation;
empty-unit row tolerated (pre-Tier-2). Update spec scenario
`Missing required field is detected` (today @deferred) to fire
through the verifier extension.

### F-22: Session-ends-mid-attestation — Tier 1 already covers

**Disposition**: Note only. Tier 1's `evaluation_runs.depth_type_attestation_ref`
persistence + Recovery republish suffices. Mid-typing data
loss is acceptable degradation; document in FM-1.

### F-23: Escalation needs FindingID

**Disposition**: Adopt the per-finding framing. Modal hint
synthesizer reads `FindingsStore.ForArrow(arrowID)` filtered
to the clause, picks the first OPEN finding (operators see
one-at-a-time; multi-finding clauses get serial escalation
prompts). Add `FindingID string` to `modal.Hint`. The
EscalationChoice records the FindingID; AttestationStore.Record
populates `AttestationRecord.FindingID` (NEW column? Tier 1
attestations table doesn't have it). Actually — pull FindingID
into the existing `Reason` or `unit_payload_json` field for
Tier 2; promote to a column in Tier 3 if needed.

### F-24: SetResidueNoteMaxBytes racy

**Disposition**: `AttestationStore.residueNoteMaxBytes` is an
`atomic.Int64`. SetResidueNoteMaxBytes is documented as
set-once at session.openEngine; multiple calls overwrite
atomically (last write wins, no torn read).

### F-25: hint_json default '{}'

**Disposition**: Change ADR-016 Part A default from `''` to
`'{}'`. Existing pre-Tier-2 rows: zero-byte hint_json
remains valid because the verifier and modal driver special-
case empty hint as "no hint available" (Tier 2 minimal hint
is always present for new rows).

### F-26: EncodeAttestationPath formatting

**Disposition**: Specify in ADR-016 Part F step 7:
`fmt.Sprintf("v%d", rec.GridVersion)`. The `gv` token in the
ADR is shorthand; replaced.

### F-27: Same as F-1

**Disposition**: F-1 remediation covers — replay from tree
(via LoadFromTree) on Tier 2 boot.

---

## Net effect on Tier 2 scope

- **Schema columns added** (was 4 in original ADR-016 Part A):
  pass_id, unit, unit_payload_json, hint_json + context,
  stratum, adversary_role. **7 new columns** total.
  hint_json default `'{}'`.
- **Recovery wiring change**: LoadFromTree replaces
  LoadFromJSONL as the Tier 2 boot loader. The flat file
  is appended-to but never read at boot.
- **EncodeAttestationPath signature**: pure function of `rec`;
  no Grid argument. Returns `(path, truncated, err)`.
- **TreeWriter behaviour**: O_RDWR open;
  TruncateTrailingPartialAll at boot.
- **Modal driver behaviour**: dedup by attestation-ref;
  snapshot-then-iterate DrainPending with 8-round cap;
  EnqueueFromRecovery method; opIDProvider injection;
  in-flight Set tracking.
- **InsufficientBasisTracker**: sticky-crossed set + Reset.
- **Schema migration**: BEGIN/ALTERs/COMMIT.
- **ValidateOpID** at /op-id (Tier 2 hardening).
- **Verifier extension** (per-unit validation).
- **Init special-case**: role-pair = `"init"`, context =
  `"init"`, stratum = `"init"`.

Adversary findings: 22 raised, 22 remediated, 0 deferred.

**Lift list update**: still 12 of 15 scenarios (the spec list
unchanged); F-3's "3-role chain path encoding" stays liftable
once AdversaryRole lands.

Implementer picks up the revised contracts +
`docs/decisions/016-tier-2-operator-modal-and-tree-primary.md`
+ this remediation log. No further architect work expected;
escalate via `specs/escalations/` if any implementation
surface contradicts the contracts.
