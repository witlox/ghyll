# Tier 2: Operator attestation flow — Go contracts

Architect output for the Tier 2 implementation phase. Every type
and function signature below traces to an invariant in
`specs/architecture/components/operator-attestation.md` or a
decision in `docs/decisions/016-tier-2-operator-modal-and-tree-primary.md`.
No method bodies — implementer fills those.

---

## `engine/store.go` — schema bump + migration

```go
const schemaVersion = 4 // was 3 (Tier 1)

// ensureUnitColumns adds pass_id / unit / unit_payload_json /
// hint_json to attestations if they don't exist yet. Idempotent;
// matches the ensureRecoverySourceColumn pattern.
func (s *Store) ensureUnitColumns() error
```

The `passes` table is unchanged.

---

## `runner/attestationstore.go` — record schema extension

```go
type AttestationRecord struct {
    // ... existing Tier-1 fields ...

    // Tier 2 additions:
    PassID          string              // links a verdict to its pass; '' for pre-Tier-2 rows
    Unit            VerdictUnit         // confirm | record-locations-inspected | write-residue-note
    UnitPayload     VerdictUnitPayload  // typed; serialized to UnitPayloadJSON at write
    UnitPayloadJSON string              // canonical JSON form persisted on disk
    HintJSON        string              // dispatcher-synthesized hint
}

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
// Path encoding lives in EncodeAttestationPath (below).
func (w *AttestationTreeWriter) PrimaryWriter(grid *Grid) func(AttestationRecord) error

// EncodeAttestationPath computes the per-pass JSONL path for
// rec under root. Returns ErrPathComponentTooLong (via the bus,
// not as an error return — the function falls back to a
// SHA-256-prefixed component and continues).
//
// Algorithm (ADR-016 Part F):
//   v<grid_version> / <context> / stratum-<stratum> / <role-pair> / <pass-id>.jsonl
//
// Role-pair construction:
//   init arrow:        "init__{target_role}"
//   2-role arrow:      "{source}__{target}"
//   3-role chain:      "{source}__{adversary}__{target}"
//
// Context segment:
//   init arrow:        "init" (literal; project-scoped)
//   non-init:          arrow.Context, filesystem-sanitized
//
// Per-component byte cap:
//   any segment > 255 bytes → replace with "h-" + sha256[:16] hex
//   AND emit ErrPathComponentTooLong via the bus.
func EncodeAttestationPath(root string, rec AttestationRecord, grid *Grid) (string, error)

var ErrPathComponentTooLong = errors.New("attestation-path-component-too-long")
```

The tree writer's existing `Observer()` method stays but is no
longer wired by `session_engine.attachJournal` (PrimaryWriter
replaces it for Tier 2).

The file name change from `<attestation_id>.jsonl` to
`<pass-id>.jsonl` means multiple verdicts on one pass append to
the same file. The existing `openCached` LRU stays; cache keys
are file paths so this naturally consolidates.

---

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
func newModalDriver(prompt modal.OperatorModalPrompt,
    store *runner.AttestationStore, passes *runner.PassRegistry,
    bus *runner.OperatorBus, ib *runner.InsufficientBasisTracker) *modalDriver

// OnEvent is the bus subscriber. Filters for
// OpEventAttestationRequested + OpEventInsufficientBasisRoundsExceeded
// and enqueues a modalRequest.
func (d *modalDriver) OnEvent(ev runner.OperatorEvent)

// DrainPending blocks until every queued request has been
// presented + answered. Each verdict turns into an
// AttestationStore.Record call; each escalation drives
// finding-status transitions + pass-abort signals.
func (d *modalDriver) DrainPending(ctx context.Context) error
```

`Session.Run` (the chat loop) calls `s.modalDriver.DrainPending(ctx)`
between user-input parsing and model-call invocation, gated on
`s.modalDriver != nil` (some test paths run without a modal).

Slash command `/op-id <new>` updates the active op-id used in
subsequent AttestationRecord.OpID values (multi-operator
handoff, F-1).

---

## `bootstrap/grid.go` — residue-note cap config

```go
type GridDefaults struct {
    // ... existing fields ...
    ResidueNoteMaxBytes int `yaml:"residue-note-max-bytes"` // default 16384
}
```

`DefaultGridDefaults()` sets `ResidueNoteMaxBytes:
DefaultMaxResidueNoteBytes` (16 KiB). `validate()` rejects
negative values. The session_engine plumbs the cap through to
`AttestationStore.SetResidueNoteMaxBytes(n)` at open.

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

Tier 2 adds:

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
)
```

---

## Enforcement map (invariants → enforcement points)

| Invariant | Enforcement |
|---|---|
| 1. Modal blocks the turn | `Session.Run` calls `modalDriver.DrainPending` pre-model. DrainPending blocks until every queued request resolves. |
| 2. Tree JSONL is primary | `attachJournal` calls `attestations.SetPrimaryWriter(treeWriter.PrimaryWriter(grid))`; the flat writer becomes Observer-only. |
| 3. Unit-conditional schema enforced | `AttestationStore.Record` calls `ValidateUnitPayload` before the primaryWriter. |
| 4. Multi-operator handoff is serial | The op-id is mutated only via `/op-id`; `modalDriver` reads the current op-id from `s.opID` at each PresentVerdict; AttestationRecord.OpID = current. |
| 5. Escalation prompt is final | `modalDriver.OnEvent` detects `OpEventInsufficientBasisRoundsExceeded`; the next presentation is `PresentEscalation` (not PresentVerdict). |
| 6. Session-ends-mid-attestation preserves the request | Tier 1's `evaluation_runs.depth_type_attestation_ref` is the persistent signal. modalDriver's `DrainPending` consults the engine table at session start to re-queue pending requests. |
| 7. Path encoding deterministic | `EncodeAttestationPath` is a pure function of (root, rec, grid). Tests verify the four-rule encoding (init, 2-role, 3-role, byte cap). |

---

## Required unit tests

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
