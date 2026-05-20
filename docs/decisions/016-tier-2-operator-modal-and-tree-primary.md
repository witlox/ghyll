# ADR-016: Operator verdict modal + tree-writer as primary

## Status

Accepted (2026-05-20). Tier 2 of the prod-readiness roadmap.
Gate-1 adversarial review of this ADR produced 22 findings (6
critical / 9 high / 7 medium); all remediated in-document.
Disposition log:
`specs/v2/validation-impl-pass-tier2-remediation.md`.

Amends:

- **ADR-015 Part C** (`015-pass-persistence-and-jsonl-source-of-truth.md`)
  — swaps which writer is the AttestationStore primary
  writer. Tier 1 wired the FLAT writer
  (`.ghyll/attestations.jsonl`). Tier 2 swaps the tree
  writer (`.ghyll/attestations/v<N>/...`) into the primary
  slot; the flat writer becomes an Observer (aggregate
  audit tail).

## Context

The analyst pass on Tier 2 (see
`specs/architecture/components/operator-attestation.md`)
captured three operator decisions:

1. The verdict UI is an interactive modal in the chat REPL —
   blocks the turn until the operator answers, with a `skip`
   option for deferral.
2. The per-pass JSONL is the load-bearing audit surface
   (`attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`).
   The flat aggregate stays as a tail.
3. The hint payload is synthesized from clause metadata
   (minimal). Producer-side richer hints are deferred to
   Tier 3.

The existing `runner.AttestationTreeWriter` (phase 9) writes a
tree but the path is keyed on `attestation_id` (per-clause file)
instead of `pass_id` (per-pass file), and `contextFromArrow` /
`stratumFromArrow` return the placeholder `"default"`. Tier 2
fixes both.

The existing `AttestationRecord` carries `attestation_id`,
`kind`, `arrow_id`, `clause_id`, `op_id`, `attested_by_role`,
`source_role`, `target_role`, `verdict`, `reason`, `timestamp`,
`grid_version`. Tier 2 adds `pass_id`, `unit`, `unit_payload_json`,
`hint_json`.

## Decision

### Part A: Schema extension + migration

Per gate-1 F-2 / F-3 / F-6 / F-10 / F-25, the migration adds
SEVEN columns (not four) and wraps the ALTERs in a single
transaction:

```sql
BEGIN;
ALTER TABLE attestations ADD COLUMN pass_id           TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN context           TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN stratum           TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN adversary_role    TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN unit              TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN unit_payload_json TEXT NOT NULL DEFAULT '';
ALTER TABLE attestations ADD COLUMN hint_json         TEXT NOT NULL DEFAULT '{}';
COMMIT;
```

- `pass_id`: which pass produced the verdict. Pre-Tier-2 rows
  have `''`; verifier-tooling tolerates the empty string;
  Tier 2 Record path REJECTS empty PassID with
  `ErrAttestationPassIDEmpty` (gate-1 F-6).
- `context`, `stratum`: stamped by the dispatcher at record-
  construction time so `EncodeAttestationPath` doesn't need a
  Grid lookup (gate-1 F-2).
- `adversary_role`: empty for normal records; populated by
  `runner/adversarial.go` when a verdict is captured during an
  adversary-phase pass (gate-1 F-3). Self-cert check extends
  to forbid `adversary_role` equal to `source_role` or
  `target_role`.
- `unit`: one of `confirm` / `record-locations-inspected` /
  `write-residue-note`. Empty for pre-Tier-2 rows.
- `unit_payload_json`: JSON object whose shape depends on
  `unit`. `'{}'` for `confirm`; `{"inspected": [...]}` for
  `record-locations-inspected`; `{"residue": "..."}` for
  `write-residue-note`.
- `hint_json`: the dispatcher-synthesized hint shown to the
  operator. **Default `'{}'`** (NOT empty string) so the
  verifier's `json.Unmarshal` parses pre-Tier-2 rows cleanly
  (gate-1 F-25). Shape: `{"arrow_id": "", "clause_id": "",
  "concept": "", "attestation_ref": ""}`. Tier 3 adds
  `locations`, `basis`, `residue`.

`schemaVersion` bumps from 3 to 4. `ensureUnitColumns` wraps
the ALTERs in `db.Begin()` / `tx.Commit()` (gate-1 F-10) so a
partial-failure rolls back cleanly. PRAGMA-based existence
check per column matches Tier 1's `ensureRecoverySourceColumn`
pattern.

### Part B: Tree writer becomes primary

`AttestationTreeWriter` gains:

- A `PrimaryWriter() func(AttestationRecord) error` method
  that mirrors `AttestationJSONLWriter.PrimaryWriter()`. No
  `*Grid` argument (gate-1 F-2 / F-19). Inline marshal + write
  + fsync; returns the error inline so
  `AttestationStore.Record` fails closed if the tree write
  fails.
- The file is opened `O_RDWR | O_APPEND` (gate-1 F-11) so the
  writer can ReadAt for `TruncateTrailingPartial`.
- A new `TruncateTrailingPartialAll(root) error` method that
  walks every `<root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl`
  and calls TruncateTrailingPartial on each. Called by
  session.openEngineWithOptions after LoadFromTree returns
  truncated=true (gate-1 F-11).
- A new `Observer()` method that mirrors the flat writer's
  flow (already exists; preserved for the rare fallback).
- Path encoding moves to a new pure function (gate-1 F-2):
  `EncodeAttestationPath(rec AttestationRecord) (path string,
  truncated bool, err error)`. The helper:
  - Reads `pass_id` (NOT `attestation_id`) for the file name.
  - Reads `context`, `stratum`, `source_role`,
    `adversary_role`, `target_role` directly from `rec`. No
    Grid lookup is required — the dispatcher stamped them at
    record-construction time.
  - Returns the tree-rooted relative path
    `v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.
  - **Init arrows** special-case (gate-1 F-18): role-pair =
    literal `"init"`, context = literal `"init"`, stratum =
    literal `"init"`. No `__` separator.
  - **Three-role chain**: when `rec.AdversaryRole != ""`,
    role-pair = `"{source}__{adversary}__{target}"` (gate-1
    F-3). Self-cert: AdversaryRole MUST NOT equal SourceRole
    or TargetRole and MUST NOT contain `__`.
  - **Empty PassID** rejects with `ErrAttestationPassIDEmpty`
    (gate-1 F-6).
  - **Empty segment** (sanitize produced empty string) also
    triggers the byte-cap fallback to a `h-<sha256[:16]>`
    name; truncated=true.
  - Per-component byte length capped at 255 (gate-1 F-17):
    overflow returns `truncated=true` AND the PrimaryWriter
    appends `path-truncated:<segment>` to
    `AttestationRecord.Reason` AND publishes
    `ErrPathComponentTooLong` via the bus. The write proceeds
    with the hash-substituted segment so the verdict is not
    lost.
  - Step 7 formatting: `fmt.Sprintf("v%d", rec.GridVersion)`
    (gate-1 F-26).

`session_engine.attachJournal` swaps the primaryWriter:

```go
// Before (Tier 1):
r.attestations.SetPrimaryWriter(r.jsonlWriter.PrimaryWriter())
r.attestations.Observe(r.treeWriter.Observer())

// After (Tier 2):
r.attestations.SetPrimaryWriter(r.treeWriter.PrimaryWriter())
r.attestations.Observe(r.jsonlWriter.Observer())
```

**Boot-time loader also swaps** (gate-1 F-1 / F-27): Tier 2's
`session.openEngineWithOptions` calls
`r.attestations.LoadFromTree(treeRoot, attCount > 0)` INSTEAD
of `LoadFromJSONL(flatPath, ...)`. The tree is the
authoritative source post-Tier 2. The flat file is
forward-only (Observer writes append; no Tier 2 path reads
from it). Tier 1's `LoadFromJSONL` stays in place for
backward-compat tests but isn't called from the production
session-open path.

`LoadFromTree` walks `<root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl`
and unions every JSONL line into `byID` via `recordReplay`
(same idempotency contract as `LoadFromJSONL`). Truncation
behavior mirrors Tier 1: `(loaded, truncated, err)` return;
truncated=true triggers a `TruncateTrailingPartialAll` pass.

`ghyll engine verify-attestations` is extended to walk BOTH
the tree AND the flat aggregate; any divergence (a tree line
not in the flat, or vice versa) is reported with
`ErrAttestationAggregateDivergence`.

### Part C: Verdict-unit schema + validation

New types:

```go
// VerdictUnit names the shape of the operator's evidence.
type VerdictUnit string

const (
    VerdictUnitConfirm                 VerdictUnit = "confirm"
    VerdictUnitRecordLocationsInspected VerdictUnit = "record-locations-inspected"
    VerdictUnitWriteResidueNote        VerdictUnit = "write-residue-note"
)

// VerdictUnitPayload is the typed shape per-unit. JSON-marshaled
// into AttestationRecord.UnitPayloadJSON at write time.
type VerdictUnitPayload struct {
    // Set when Unit == record-locations-inspected.
    Inspected []string `json:"inspected,omitempty"`
    // Set when Unit == write-residue-note.
    Residue string `json:"residue,omitempty"`
}

// MaxResidueNoteBytes caps a write-residue-note payload at
// 16 KiB by default. Operator-configurable via the grid file's
// `residue-note-max-bytes` setting.
const DefaultMaxResidueNoteBytes = 16 * 1024

var (
    ErrVerdictUnitInvalid       = errors.New("verdict-unit-invalid")
    ErrVerdictUnitMissingField  = errors.New("verdict-unit-missing-field")
    ErrVerdictResidueTooLong    = errors.New("verdict-residue-too-long")
    ErrVerdictInspectedEmpty    = errors.New("verdict-inspected-empty")
)

// ValidateUnitPayload returns nil iff payload satisfies the
// unit's schema. Called at the AttestationStore.Record boundary
// BEFORE the primaryWriter fires.
func ValidateUnitPayload(unit VerdictUnit, p VerdictUnitPayload, maxResidueBytes int) error
```

`AttestationStore.Record` validates the unit before calling
the primary writer. A schema failure returns the typed error;
no JSONL line is written; no in-memory mutation happens.

### Part D: Operator modal driver

New package: `cmd/ghyll/modal` (or inline in `cmd/ghyll/session.go`
— see architect note below). Exports:

```go
// OperatorModalPrompt is the contract the chat REPL uses to
// present a verdict modal. Implementations: TermModal (tty
// interactive); StubModal (test injection); ScriptedModal
// (BDD replay from a fixture).
type OperatorModalPrompt interface {
    // PresentVerdict blocks until the operator submits a verdict
    // or returns ErrModalSkipped. The Hint argument carries the
    // dispatcher-synthesized payload. Returns the verdict + unit
    // + payload, ready to feed into AttestationStore.Record.
    PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error)

    // PresentEscalation blocks until the operator chooses option 1
    // (accepted-risk + residue note) or option 2 (route-upstream +
    // rationale). No default; no skip.
    PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error)
}

type Hint struct {
    ArrowID        string
    ClauseID       string
    Concept        string
    AttestationRef string
}

type VerdictSubmission struct {
    Verdict runner.AttestationVerdict
    Unit    runner.VerdictUnit
    Payload runner.VerdictUnitPayload
}

type EscalationChoice struct {
    Option  int // 1 = accepted-risk; 2 = route-upstream
    Residue string
}

var ErrModalSkipped = errors.New("modal-skipped")
```

The modal driver wires:

```go
// In cmd/ghyll/session.go, post-attachJournal:
s.modalDriver = newModalDriver(s.modalPrompt, s.engine.AttestationStore(),
    s.engine.Passes(), s.engine.Bus(), s.engine.IBTracker())

// modalDriver subscribes the bus.
s.engine.Bus().Subscribe(s.modalDriver.OnEvent)
```

`modalDriver.OnEvent` filters for `OpEventAttestationRequested`
and `OpEventInsufficientBasisRoundsExceeded`. On either, it
queues a modal-presentation request that the REPL turn loop
drains BEFORE the next model call.

### Part E: REPL turn-loop integration

`Session.Run` (the chat loop) gets a new pre-turn check:

```go
for {
    line := s.readInput()
    if handled := s.dispatchSlashCommand(line); handled.Handled {
        // ... existing slash handling ...
        continue
    }
    // NEW: drain pending modal prompts before the model call.
    if err := s.modalDriver.DrainPending(ctx); err != nil {
        s.output(fmt.Sprintf("⚠ modal error: %v", err))
    }
    // ... existing model invocation ...
}
```

`DrainPending` blocks until all queued modal-presentation
requests are answered (or the operator types `skip` for each).
Each Present* call records via `AttestationStore.Record`; the
tree writer's PrimaryWriter persists; the chat loop continues.

### Part F: Path encoding precision

`EncodeAttestationPath` algorithm (Part B above expands here):

```
input:  AttestationRecord rec, Grid g (for arrow lookup)
output: path string, error

1. Resolve arrow def via g.Lookup(rec.ArrowID).
   If not found → use rec.SourceRole / rec.TargetRole as
   fallback (recovery path may run before grid replay
   completes; defensive).

2. Determine role-pair string:
   a. If arrow def has 3-role chain (adversary-augmented):
      "{source}__{adversary}__{target}".
   b. If init arrow: "init__{target_role}" (init has no
      source role in the chain).
   c. Otherwise: "{source}__{target}".

3. Determine context segment:
   a. If init arrow: literal "init".
   b. Else: arrow def's Context field (sanitized for
      filesystem: replace any of [/\\:*?<>|] with "_").

4. Determine stratum segment:
   "stratum-" + sanitized(arrow_def.Stratum).

5. Pass-id segment: sanitized(rec.PassID) + ".jsonl".

6. Per-component byte-length check (255-byte cap per ext4):
   if any component exceeds 255 bytes, replace it with
   "h-" + first 16 hex bytes of sha256(original). Emit
   ErrPathComponentTooLong via the bus.

7. Return filepath.Join(treeRoot, "v"+gv, ctx, stratum,
   rolePair, passFile).
```

Tier 3-augmented arrows (adversary phase added mid-pass)
can change the role-pair between rounds. The path encoded
at verdict-time captures the role chain AT VERDICT TIME;
the tree may contain multiple files for the same logical
"pass" if the chain mutated. Operator-tooling can join via
the aggregate JSONL (which has all lines regardless of
tree path).

### Part G: hint synthesis (Tier 2 minimal)

`runner.SynthesizeHint(clause, arrow_def) Hint`:

```go
func SynthesizeHint(c Clause, def ArrowDefinition) Hint {
    return Hint{
        ArrowID:        def.ID,
        ClauseID:       c.ClauseID,
        Concept:        c.Concept,
        AttestationRef: c.DepthTypeAttestationRef,
    }
}
```

The dispatcher calls this when publishing
`OpEventAttestationRequested` and stores the resulting
JSON in the (eventual) `attestation_requests` table or
inlines it on the bus event. **Decision (post-architect
review)**: inline on the event for Tier 2; promote to a
persisted table only if Tier 3's session-ends-mid-attestation
flow needs it (Tier 1's `evaluation_runs.depth_type_attestation_ref`
is enough for the basic recovery flow).

## Consequences

Positive:

- 12 of 15 attestation.feature `@deferred` scenarios become
  liftable.
- The operator gets a real verdict UX, not a power-user
  slash command.
- The per-pass tree audit is structurally sound (matches
  the scenarios' literal text).
- Unit-conditional validation closes the gate-1 F-1 finding
  about missing required fields.
- ADR-015 Part C's "JSONL is source of truth" invariant is
  preserved — just shifted to the tree.

Negative:

- ADR-015 amended again (now ADR-016 amends Part C). Operators
  inspecting attestations should consult the tree as primary
  and the flat as aggregate.
- Schema migration runs at the v3→v4 boundary. Older binaries
  refuse to open v4 stores (verifySchemaVersion).
- The chat REPL gains a new blocking point. Operators who
  expected uninterruptible turn loops will be surprised.
- Test depth increases: every BDD scenario in F-1 through
  F-4 needs a ScriptedModal injection point.
- Path-encoding edge cases (255-byte cap, sanitization,
  three-role chains) require careful test coverage.
- Concurrent operator scenario stays deferred (Tier 3+).

## Alternatives considered

**Alt 1: Keep flat as primary, add a tree-rebuild CLI.** Tier 2
would not swap PrimaryWriter; the flat continues as
source-of-truth; `ghyll engine rebuild-tree` materializes the
tree as needed for forensic review. Rejected — the scenarios
explicitly assert the per-pass tree is the primary write
surface ("a record is appended to ... attestations/...").

**Alt 2: Add a separate AttestationStore-per-pass.** One
in-memory store per pass, each with its own primary writer.
Rejected — multiplies the runner-layer state; the existing
single AttestationStore with multi-PrimaryWriter chains
doesn't exist (PrimaryWriter is single-valued) but the tree
writer's path-encoding gives the per-pass-ness without state
multiplication.

**Alt 3: Defer the modal; ship the slash command only.**
Operators type `/attest <ref> pass --unit confirm`. Rejected
— the F-3 / F-4 escalation flow with "operator must choose
between option 1 and option 2" doesn't work via slash
command; you can't make a slash command non-defaulted.
Modal is needed for the escalation prompt at minimum.

## Implementation seam

- `engine/store.go`: `schemaVersion = 4`;
  `ensureUnitColumns()` migration mirrors
  `ensureRecoverySourceColumn`.
- `engine/records.go`: new fields on `AttestationRecord` (in
  the engine variant — runner's `runner.AttestationRecord`
  mirrors).
- `runner/attestationstore.go`: `AttestationRecord` gains
  `PassID`, `Unit`, `UnitPayload`, `HintJSON`.
  `Validate(rec)` calls `ValidateUnitPayload`.
- `runner/attestation_tree.go`:
  - File name is `<sanitized(rec.PassID)>.jsonl` (was
    `<rec.ID>.jsonl`).
  - `contextFromArrow` + `stratumFromArrow` use the grid
    lookup; placeholder removed.
  - Add `PrimaryWriter()` method.
  - Implement `EncodeAttestationPath`.
- `runner/dispatcher.go`: `SynthesizeHint(c, def)` + inline
  in the `Publish(OpEventAttestationRequested)` call.
- `cmd/ghyll/session.go`: subscribe modalDriver; new
  `DrainPending` pre-turn step.
- `cmd/ghyll/modal/` (new package): `OperatorModalPrompt`
  interface + `TermModal` impl + `StubModal` for tests.
- `tests/acceptance/`: BDD step bindings use `StubModal` /
  `ScriptedModal` to drive verdicts.

## References

- `specs/architecture/components/operator-attestation.md` (analyst output)
- ADR-010 (the original engine-table-is-source-of-truth, amended in Tier 1)
- ADR-015 (Tier 1 — pass persistence + JSONL flat as primary)
- `specs/features/attestation.feature` (the 12 scenarios this enables)
