# Tier 2: Operator attestation flow — Go contracts

Architect output for the Tier 2 implementation phase. Every type
and function signature below traces to an invariant in
`specs/architecture/components/operator-attestation.md` or a
decision in `docs/decisions/016-tier-2-operator-modal-and-tree-primary.md`.
No method bodies — implementer fills those.

**Gate-1 adversary review** (2026-05-20) produced 22 findings;
all remediated in this document, the spec, and the ADR. See
`specs/v2/validation-impl-pass-tier2-remediation.md` for the
disposition log; F-N refs below cite the finding being addressed.

---

## `engine/store.go` — baseline schema

The 7 Tier 2 columns (pass_id, context, stratum, adversary_role,
unit, unit_payload_json, hint_json) are part of the baseline
`CREATE TABLE attestations` along with the `CHECK (pass_id <> '')`
constraint.

```go
const schemaVersion = 1
```

Pre-prod the v2→v5 ALTER chain that originally introduced these
columns was collapsed into a single fresh schema (no upgrade path
is preserved). The `passes` table is unchanged.

---

## `runner/attestationstore.go` — record schema extension

```go
type AttestationRecord struct {
    // ... existing Tier-1 fields ...

    // Tier 2 additions (gate-1 remediation expanded the original
    // 4 fields to 7):
    PassID          string              // gate-1 F-6: REQUIRED at write; '' rejects
    Context         string              // gate-1 F-2: stamped by dispatcher; pure path-encode input
    Stratum         string              // gate-1 F-2: stamped by dispatcher; pure path-encode input
    AdversaryRole   string              // gate-1 F-3: populated only during adversary-phase verdicts
    Unit            VerdictUnit         // confirm | record-locations-inspected | write-residue-note
    UnitPayload     VerdictUnitPayload  // typed; serialized to UnitPayloadJSON at write
    UnitPayloadJSON string              // canonical JSON form persisted on disk
    HintJSON        string              // dispatcher-synthesized hint; default '{}' (gate-1 F-25)
}

var ErrAttestationPassIDEmpty       = errors.New("attestation-pass-id-empty")
var ErrAttestationAggregateDivergence = errors.New("attestation-aggregate-divergence")

type VerdictUnit string

const (
    VerdictUnitConfirm                  VerdictUnit = "confirm"
    VerdictUnitRecordLocationsInspected VerdictUnit = "record-locations-inspected"
    VerdictUnitWriteResidueNote         VerdictUnit = "write-residue-note"
)

type VerdictUnitPayload struct {
    Inspected []string `json:"inspected,omitempty"` // for record-locations-inspected
    Residue   string   `json:"residue,omitempty"`   // for write-residue-note
}

const DefaultMaxResidueNoteBytes = 16 * 1024

var (
    ErrVerdictUnitInvalid      = errors.New("verdict-unit-invalid")
    ErrVerdictUnitMissingField = errors.New("verdict-unit-missing-field")
    ErrVerdictResidueTooLong   = errors.New("verdict-residue-too-long")
    ErrVerdictInspectedEmpty   = errors.New("verdict-inspected-empty")
)

// ValidateUnitPayload returns nil iff payload satisfies the
// unit's required-field schema. Called inside Record before
// the primaryWriter fires.
//
//   confirm                      → payload must be zero-value
//   record-locations-inspected   → Inspected must be non-empty
//   write-residue-note           → Residue non-empty + ≤ maxResidueBytes
//
// maxResidueBytes is the project-configured cap (default
// DefaultMaxResidueNoteBytes).
func ValidateUnitPayload(u VerdictUnit, p VerdictUnitPayload, maxResidueBytes int) error
```

`AttestationStore.Record` calls `ValidateUnitPayload` BEFORE the
primaryWriter, with the project's configured cap (default
16KB). The cap is read from `bootstrap.GridDefaults.ResidueNoteMaxBytes`
(new field; default = DefaultMaxResidueNoteBytes).

---

## `runner/attestation_tree.go` — primary writer + path encoding

```go
// PrimaryWriter returns a func suitable for
// AttestationStore.SetPrimaryWriter. Tier 2 (ADR-016 Part B):
// the tree writer becomes the inline-blocking audit surface;
// the flat writer steps down to Observer.
//
// No Grid argument (gate-1 F-2 / F-19): EncodeAttestationPath
// reads all data from `rec` (context/stratum/source/adversary/
// target/pass-id are stamped at record construction).
//
// On gate-1 F-17: when EncodeAttestationPath returns
// truncated=true (a component overflowed 255 bytes or was
// empty), PrimaryWriter appends "path-truncated:<segment>" to
// rec.Reason BEFORE the JSONL marshal, publishes
// ErrPathComponentTooLong via the bus, and proceeds with the
// hash-substituted segment.
func (w *AttestationTreeWriter) PrimaryWriter() func(AttestationRecord) error

// TruncateTrailingPartialAll walks every per-pass JSONL file
// under root and calls TruncateTrailingPartial on each.
// Called by session.openEngineWithOptions after LoadFromTree
// returns truncated=true (gate-1 F-11).
func (w *AttestationTreeWriter) TruncateTrailingPartialAll(root string) error

// EncodeAttestationPath is a pure function of rec (gate-1 F-2).
//
// Algorithm (ADR-016 Part F, gate-1-remediated):
//   v<grid_version> / <context> / stratum-<stratum> / <role-pair> / <pass-id>.jsonl
//
// Role-pair construction:
//   init arrow (rec.AttestedByRole == "init"):
//     role-pair  = "init"
//     context    = "init"
//     stratum    = "init"
//   3-role chain (rec.AdversaryRole != ""):
//     role-pair  = "{source}__{adversary}__{target}"
//   2-role arrow:
//     role-pair  = "{source}__{target}"
//
// Empty rec.PassID returns ErrAttestationPassIDEmpty (gate-1 F-6).
//
// Per-component byte cap (255 bytes ext4 / btrfs / NTFS code units):
//   any segment > 255 bytes OR empty after sanitize → replace with
//   "h-" + first 16 hex bytes of sha256(original). Sets
//   truncated=true; the PrimaryWriter publishes
//   ErrPathComponentTooLong via the bus.
//
// Step 7 formatting (gate-1 F-26):
//   gv segment = fmt.Sprintf("v%d", rec.GridVersion).
func EncodeAttestationPath(rec AttestationRecord) (path string, truncated bool, err error)

var (
    ErrPathComponentTooLong = errors.New("attestation-path-component-too-long")
    ErrAttestationGridNotAvailable = errors.New("attestation-grid-not-available") // gate-1 F-19
)
```

The tree writer's existing `Observer()` method stays but is no
longer wired by `session_engine.attachJournal` (PrimaryWriter
replaces it for Tier 2).

The file name change from `<attestation_id>.jsonl` to
`<pass-id>.jsonl` means multiple verdicts on one pass append to
the same file. The existing `openCached` LRU stays; cache keys
are file paths so this naturally consolidates.

---

## `runner/attestationstore.go` — boot loader from tree

Tier 2 (gate-1 F-1 / F-27 remediation): the tree is the
authoritative load surface, not the flat aggregate.

```go
// LoadFromTree walks <root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl
// and populates byID via recordReplay. Mirrors LoadFromJSONL's
// LoadResult shape; truncated=true triggers a
// TruncateTrailingPartialAll(root) sweep at the caller side
// (session.openEngineWithOptions).
//
// engineHasRows distinguishes the fresh-project case (no tree,
// no engine rows = OK) from the broken-audit case (no tree but
// the engine has attestation rows = ErrAttestationAuditLost).
//
// The flat aggregate (.ghyll/attestations.jsonl) is NOT read
// at boot post-Tier-2; it's a forward-only Observer surface.
func (s *AttestationStore) LoadFromTree(root string, engineHasRows bool) (LoadResult, error)
```

The pre-existing `LoadFromJSONL` stays for backward-compat
tests; production session-open path uses `LoadFromTree`.

## `runner/dispatcher.go` — hint synthesis

```go
// Hint carries the dispatcher-synthesized payload presented to
// the operator inside the verdict modal. Tier 2 minimal shape;
// Tier 3 can add Locations / Basis / Residue from a producer
// hook.
type Hint struct {
    ArrowID        string `json:"arrow_id"`
    ClauseID       string `json:"clause_id"`
    Concept        string `json:"concept"`
    AttestationRef string `json:"attestation_ref"`
}

// SynthesizeHint returns the Tier 2 minimal hint for a clause.
func SynthesizeHint(c Clause) Hint
```

The dispatcher's `Publish(OpEventAttestationRequested)` call
(Tier 1 batch 1 / M-3 remediation) now serializes the Hint into
the event's Detail field as canonical JSON. The modal driver
deserializes when presenting.

---

## `cmd/ghyll/modal/` — new package

```go
// Package modal provides the operator verdict modal contract +
// implementations. Tier 2 (ADR-016 Part D).

// OperatorModalPrompt is the chat REPL's interactive interrupt.
type OperatorModalPrompt interface {
    PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error)
    PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error)
}

type Hint struct {
    ArrowID, ClauseID, Concept, AttestationRef string
}

type VerdictSubmission struct {
    Verdict runner.AttestationVerdict
    Unit    runner.VerdictUnit
    Payload runner.VerdictUnitPayload
}

type EscalationChoice struct {
    Option  int    // 1 = accepted-risk; 2 = route-upstream
    Residue string // required
}

var ErrModalSkipped = errors.New("modal-skipped")

// TermModal is the tty-interactive implementation. Reads from
// stdin, writes prompts to stdout. Blocks the REPL's turn loop
// until the operator answers.
type TermModal struct {
    In  io.Reader
    Out io.Writer
}

func (m *TermModal) PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error)
func (m *TermModal) PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error)

// StubModal is the test-injectable implementation. Pre-loaded
// with scripted responses; each Present* call consumes the
// next response from the queue.
type StubModal struct {
    Verdicts     []VerdictSubmission
    Escalations  []EscalationChoice
    SkipVerdicts []bool
}

func (m *StubModal) PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error)
func (m *StubModal) PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error)
```

---

## `cmd/ghyll/session.go` — modal driver wiring

```go
// modalDriver wires modal presentation to the operator bus +
// AttestationStore. Subscribes to OpEventAttestationRequested
// and OpEventInsufficientBasisRoundsExceeded; queues modal
// requests that the REPL turn loop drains via DrainPending.
type modalDriver struct {
    prompt       modal.OperatorModalPrompt
    attestations *runner.AttestationStore
    passes       *runner.PassRegistry
    bus          *runner.OperatorBus
    ibTracker    *runner.InsufficientBasisTracker

    mu      sync.Mutex
    pending []modalRequest
}

type modalRequest struct {
    kind   string // "verdict" | "escalation"
    hint   modal.Hint
    passID string
}

// newModalDriver constructs + subscribes to the bus.
//
// opIDProvider (gate-1 F-20) returns the active op-id at
// CALL TIME (NOT enqueue time). Implementations typically
// inject `func() string { return s.opID }`. AttestationRecord.OpID
// is populated from this provider inside PresentVerdict /
// PresentEscalation (i.e. at the moment the operator
// submits, not at modal enqueue).
//
// findings (gate-1 F-23) is consulted by the hint synthesizer
// when an escalation modal needs to surface the specific
// finding being attested.
func newModalDriver(prompt modal.OperatorModalPrompt,
    store *runner.AttestationStore, passes *runner.PassRegistry,
    bus *runner.OperatorBus, ib *runner.InsufficientBasisTracker,
    findings *runner.FindingsStore,
    opIDProvider func() string) *modalDriver

// OnEvent is the bus subscriber. Filters for
// OpEventAttestationRequested + OpEventInsufficientBasisRoundsExceeded
// and enqueues a modalRequest.
//
// Gate-1 F-12 dedup: the driver maintains an inFlight set
// keyed on the AttestationRecord.ID; OnEvent drops events
// for an already-in-flight ref.
func (d *modalDriver) OnEvent(ev runner.OperatorEvent)

// EnqueueFromRecovery folds the post-Recovery event list into
// the modal driver's pending queue (gate-1 F-4). Called from
// session.go:initEngine after attachJournal + bus.Subscribe
// have completed. Filters for OpEventRecoveryAttestationRepublished;
// constructs Hints from ArrowID + ClauseID + Detail att-ref
// embed.
func (d *modalDriver) EnqueueFromRecovery(events []runner.OperatorEvent)

// DrainPending blocks until every queued request has been
// presented + answered. Each verdict turns into an
// AttestationStore.Record call; each escalation drives
// finding-status transitions + pass-abort signals.
//
// Gate-1 F-5 / F-8 remediation: DrainPending takes a snapshot
// of `pending` under d.mu, drops the lock, iterates the
// snapshot. New OnEvent enqueues to a fresh list; after one
// snapshot drains, re-snapshot. Cap at 8 drain rounds per
// turn; over-cap returns ErrModalDrainCapExceeded.
func (d *modalDriver) DrainPending(ctx context.Context) error

var (
    ErrModalDrainCapExceeded = errors.New("modal-drain-cap-exceeded")
    ErrOpIDInvalidCharacters = errors.New("op-id-invalid-characters") // gate-1 F-13
)
```

`Session.Run` (the chat loop) calls `s.modalDriver.DrainPending(ctx)`
between user-input parsing and model-call invocation, gated on
`s.modalDriver != nil` (some test paths run without a modal).

Slash command `/op-id <new>` updates the active op-id used in
subsequent AttestationRecord.OpID values (multi-operator
handoff, F-1).

---

## `bootstrap/grid.go` — residue-note cap config + modal queue cap

```go
type GridDefaults struct {
    // ... existing fields ...
    ResidueNoteMaxBytes int `yaml:"residue-note-max-bytes"` // default 16384 (gate-1 F-9)
    ModalPendingMaxLen  int `yaml:"modal-pending-max-len"`  // default 64    (gate-1 F-8)
}
```

`DefaultGridDefaults()` sets:
- `ResidueNoteMaxBytes: DefaultMaxResidueNoteBytes` (16 KiB).
- `ModalPendingMaxLen: 64`.

`validate()` rejects `<= 0` for both (gate-1 F-9). Existing v1
/ v2 grid files lack these YAML keys; the YAML decoder produces
a zero value; the existing `bootstrap/grid.go:108` post-decode
normalize step substitutes the default. No breakage on legacy
files.

The session_engine plumbs both caps through:
- `AttestationStore.SetResidueNoteMaxBytes(n)` — backed by
  `atomic.Int64` (gate-1 F-24); set-once at open, multiple
  calls overwrite atomically without torn reads.
- modal driver constructed with the queue cap (gate-1 F-8);
  overflow drops + publishes OpEventModalBackpressure.

---

## `runner/attestationstore.go` — residue cap accessor

```go
// SetResidueNoteMaxBytes configures the unit-payload validation
// cap for write-residue-note verdicts. Default
// DefaultMaxResidueNoteBytes.
func (s *AttestationStore) SetResidueNoteMaxBytes(n int)
```

---

## OperatorBus event kinds

Tier 2 adds (post gate-1, two additions for backpressure +
path-overflow):

```go
const (
    // OpEventClauseFailVerdict — operator submitted verdict=fail
    // on an attested clause. Subscribed by the producer-fix
    // signal path so the upstream role gets a remediation
    // trigger.
    OpEventClauseFailVerdict OperatorEventKind = "clause-fail-verdict"

    // OpEventEscalationPresented — the modal showed an
    // escalation prompt to the operator. Audit-trail only.
    OpEventEscalationPresented OperatorEventKind = "escalation-presented"

    // OpEventEscalationResolved — operator chose option 1 or 2
    // on the escalation prompt. Detail carries the choice.
    OpEventEscalationResolved OperatorEventKind = "escalation-resolved"

    // OpEventModalSkipped — operator typed `skip` on the
    // verdict modal; the clause stays pending. Lock token is
    // released so the dispatcher can move on; the next REPL
    // turn re-presents on OpEventAttestationRequested
    // republish.
    OpEventModalSkipped OperatorEventKind = "modal-skipped"

    // OpEventModalBackpressure — modal driver dropped an
    // OnEvent because its pending queue is at
    // ModalPendingMaxLen (gate-1 F-8).
    OpEventModalBackpressure OperatorEventKind = "modal-backpressure"

    // OpEventPathTruncated — EncodeAttestationPath produced
    // a path with one or more hash-substituted segments
    // (gate-1 F-17). Audit-trail; the write still succeeded.
    OpEventPathTruncated OperatorEventKind = "attestation-path-truncated"
)
```

---

## Enforcement map (invariants → enforcement points, gate-1-remediated)

| Invariant | Enforcement |
|---|---|
| 1. Modal blocks the turn | `Session.Run` calls `modalDriver.DrainPending` pre-model. Snapshot-then-iterate; 8-round cap; ctx-cancel returns context.Canceled (gate-1 F-5, F-14). |
| 2. Tree JSONL is primary AND boot loader | `attachJournal` calls `attestations.SetPrimaryWriter(treeWriter.PrimaryWriter())` (no Grid arg per F-2). `openEngineWithOptions` calls `LoadFromTree` at boot (NOT `LoadFromJSONL` of the flat file) per gate-1 F-1 / F-27. |
| 3. Unit-conditional schema enforced | `AttestationStore.Record` calls `ValidateUnitPayload` before the primaryWriter; verifier extension validates per-unit payload (gate-1 F-21). |
| 4. Multi-operator handoff is serial | The op-id is mutated only via `/op-id`; `modalDriver` calls `opIDProvider()` at PresentVerdict-call time (gate-1 F-20); AttestationRecord.OpID = current. ValidateOpID rejects path-unsafe characters (gate-1 F-13). |
| 5. Escalation prompt is final | `InsufficientBasisTracker.crossedClauses` is a sticky set (gate-1 F-7); every IB Record on a crossed clause re-emits OpEventInsufficientBasisRoundsExceeded. modalDriver dedups via inFlight set (gate-1 F-12) so the operator sees one prompt at a time. |
| 6. Session-ends-mid-attestation preserves the request | Tier 1's `evaluation_runs.depth_type_attestation_ref` persists across crash. session.go:initEngine calls `modalDriver.EnqueueFromRecovery(rt.RecoveryReport().Events)` after attachJournal (gate-1 F-4) so the modal driver explicitly consumes the recovery events. |
| 7. Path encoding deterministic | `EncodeAttestationPath` is a pure function of `rec` (no Grid arg per gate-1 F-2). Tests verify: init special-case (gate-1 F-18), 2-role, 3-role chain (gate-1 F-3), empty-PassID rejection (gate-1 F-6), empty-segment hash fallback (gate-1 F-6/F-17), byte cap (gate-1 F-17). |

---

## Verifier extension (gate-1 F-21)

`runner/attestation_verifier.go` extends to validate Tier 2
columns:

- If `unit == "record-locations-inspected"`: validate
  `unit_payload_json` parses to `{"inspected": [...]}` with
  non-empty array; error: `ErrVerifyMissingInspected`.
- If `unit == "write-residue-note"`: validate
  `unit_payload_json` parses to `{"residue": "..."}` with
  non-empty, ≤ ResidueNoteMaxBytes string; error:
  `ErrVerifyResidueOversizedOrEmpty`.
- If `unit == "confirm"`: validate `unit_payload_json` is
  `'{}'` or `''` (back-compat with pre-Tier-2 empty); error:
  `ErrVerifyConfirmHasPayload`.
- If `unit == ""`: pre-Tier-2 row; skip unit validation.

Tree-vs-flat divergence detection (gate-1 F-1 follow-up):
`AttestationVerifier.VerifyAll(treeRoot, flatPath)
(VerifyResult, error)` walks both surfaces; emits
`ErrAttestationAggregateDivergence` when a line exists in one
but not the other.

---

## Required unit tests

Gate-1 remediation added the bottom 10 (T-12 onward):

- `TestVerdictUnit_ValidateConfirm_AcceptsEmpty` + `RejectsExtraFields`
- `TestVerdictUnit_ValidateRecordLocations_AcceptsList` + `RejectsEmpty`
- `TestVerdictUnit_ValidateWriteResidueNote_AcceptsUnderCap` +
  `RejectsOverCap` + `RejectsEmpty`
- `TestEncodeAttestationPath_TwoRole`
- `TestEncodeAttestationPath_ThreeRoleChain` (`analyst__adversary__architect`)
- `TestEncodeAttestationPath_Init` (`init__analyst` + literal `init` context)
- `TestEncodeAttestationPath_ByteCapOverflow` (255-byte component → SHA prefix)
- `TestAttestationTreeWriter_PrimaryWriter_FailsClosed` (disk full
  → ErrAttestationAuditWriteFailed; in-memory unchanged)
- `TestAttestationStore_Record_RejectsInvalidUnit`
- `TestModalDriver_DrainPending_PresentsAndRecords`
- `TestModalDriver_OnEvent_EscalationOverridesVerdict`
- `TestSchemaMigration_V3ToV4` (PRAGMA shows new columns)
- `TestSchemaMigration_V3ToV4_PartialFailureRollsBack` (gate-1 F-10)
- `TestEncodeAttestationPath_EmptyPassIDRejected` (gate-1 F-6)
- `TestEncodeAttestationPath_AdversaryRoleSelfCertRejected` (gate-1 F-3)
- `TestLoadFromTree_WalksAllVersions` (gate-1 F-1)
- `TestLoadFromTree_TruncatedTrailingLine` (gate-1 F-11)
- `TestTreeWriter_TruncateTrailingPartialAll` (gate-1 F-11)
- `TestModalDriver_EnqueueFromRecovery_DeduplicatesAgainstBusEvent` (gate-1 F-12, F-4)
- `TestModalDriver_DrainPendingCapped` (gate-1 F-5)
- `TestInsufficientBasisTracker_StickyCrossed` (gate-1 F-7)
- `TestValidateOpID_RejectsPathTraversal` (gate-1 F-13)
- `TestModalAtExit_ContextCancellation` (gate-1 F-14)
- `TestResidueNoteMaxBytes_AtomicSetOnce` (gate-1 F-24)
- `TestEngineVerifyAttestations_DetectsAggregateDivergence` (gate-1 F-1 follow-up)
- BDD bindings for the 12 attestation.feature scenarios (in
  `tests/acceptance/steps_tier2_modal.go`).

---

## Spec gap escalations (architect → analyst, if any)

None for Tier 2 — the analyst spec is structurally sufficient
for the architect to specify all type signatures + invariants.

Open items the architect flags for the operator's review (NOT
blocking implementer):

- **Modal package location**: inline in `cmd/ghyll/session.go`
  vs. new `cmd/ghyll/modal/`. Recommend new package for
  testability; the implementer makes the call.
- **Tier 3 hint hook**: Producer-side richer hints needs a
  `Producer.EmitHint(clause) Hint` interface. Tier 2 ships
  with the minimal SynthesizeHint; Tier 3's architect picks
  this up.

---

## Handoff to adversary (gate 1)

Adversary should probe (cold-context review of this contracts
doc + the analyst spec):

1. **Modal blocking during a streaming model call**. The
   contract says DrainPending runs pre-model. What if a model
   call is in-flight (Tier 2 dispatcher races the modal
   request)? Can the modal interrupt a streaming call?

2. **Modal driver bus subscription**. The bus today fires
   synchronously to subscribers; `OnEvent` enqueues. What if
   the queue grows unboundedly between turns? Backpressure?

3. **Tree-writer-as-primary inversion blast radius**. ADR-015
   Part C set the flat writer as primary. ADR-016 Part B swaps
   to the tree writer. Enumerate every caller of
   `AttestationStore.SetPrimaryWriter` and every code path that
   reads the flat file as authoritative (gate-1 audit of
   ADR-015 caught 4; verify Tier 2 doesn't reintroduce).

4. **Path encoding edge cases**: arrows whose context name
   contains `/` (a literal forward slash → path traversal?).
   The sanitizer maps `/` to `_`, but what if the operator
   declares a context literally named `..`? Or `.`? Or empty
   string? Path encoding must be hostile-input-safe.

5. **Schema migration on partial state**. v3 store has
   attestation rows with empty pass_id (pre-Tier-2). The new
   migration ALTERs with DEFAULT ''. Tier 2 code reads
   PassID — what happens with empty PassID in
   EncodeAttestationPath? The file becomes `.jsonl` (no name).
   Verify the fallback (the sanitizer empties the path
   component, then the byte cap path kicks in producing a
   hash → file is `h-<digest>.jsonl`). OK but ugly.

6. **VerdictUnit validation timing**. ValidateUnitPayload runs
   INSIDE Record's critical section. If validation is slow
   (e.g., a 16KB residue scan), the AttestationStore.mu stays
   held. Tighten the bound: cap residue scan time.

7. **Concurrent modal + slash command**. Operator types
   `/attest <ref> pass` while the modal is open. Who wins?
   The modal's queued request reads its own pending list; the
   slash command goes directly to AttestationStore.Record. Two
   writes for the same attestation_id → ErrAttestationDuplicate
   on the second. Which one fires first depends on goroutine
   scheduling.

8. **Three-role chain detection**. Today's `ArrowDefinition`
   has SourceRole + TargetRole. Where does the
   `analyst__adversary__architect` chain come from? The
   adversary phase doesn't extend the arrow def; it adds
   findings. The role-pair is heuristic: if the arrow's
   adversarial phase ran, the chain is 3-role. Operator-tooling
   needs to consult the arrow's pass history to know. Trace
   how EncodeAttestationPath gets this signal.

9. **modalDriver.DrainPending error paths**: PresentVerdict
   returns ErrModalSkipped, PresentEscalation has no skip.
   What if PresentEscalation returns an unexpected error
   (network EOF, ctx cancelled)? DrainPending propagates;
   the REPL turn aborts. The pending escalation stays queued;
   next turn re-presents.

10. **ResidueNoteMaxBytes config drift**. The cap is in the
    grid file. Tier 2's `SetResidueNoteMaxBytes(n)` is called
    at session.openEngine. Operator edits the grid file
    mid-session (changes the cap from 16KB → 4KB) — does the
    in-memory store see the change? Recommend: cap is read
    once at session start; mid-session changes apply on next
    restart.

Adversary writes findings to
`/home/witlox/ghyll/specs/v2/validation-impl-pass-tier2.md`.
