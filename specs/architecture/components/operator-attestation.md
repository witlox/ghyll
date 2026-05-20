# Component: operator attestation flow

The runtime surface that connects a clause's attestation request
to a human operator's verdict. Tier 2 of the prod-readiness
roadmap. Builds on Tier 1's persistence + recovery substrate
(ADR-015) but introduces three new surfaces:

1. **Interactive modal prompt** in the chat REPL.
2. **Tree-shaped per-pass JSONL** (`attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`)
   becomes the load-bearing audit surface.
3. **Unit-conditional verdict schema** with required-field validation.

> Status: design intent (analyst pass).
> Predecessor: `state-machine.md`, `pass-persistence.md`.
> Architect: needs an ADR for the modal-in-chat-loop interruption
> contract + the tree-writer-as-primary-writer choice (amends
> the ADR-015 Part C inversion which today targets the flat file).

---

## Scope

**In scope.**

- Operator session lifecycle: declare op-id, hand off (Alice
  ends, Bob starts), session-ends-mid-attestation.
- Modal verdict prompt in the chat REPL: when the dispatcher
  detects a pending attestation, the REPL interrupts the
  normal turn and presents a verdict modal. Unit selection +
  payload capture happen inside the modal.
- Three verdict units: `confirm`, `record-locations-inspected`,
  `write-residue-note`. Each has its own required-fields
  schema; submission with a missing field rejects with a
  typed error.
- Tree-writer-as-primary: every verdict appends to the
  per-pass JSONL file under `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.
  Path encoding: `__` separator between roles, `init__analyst`
  for the init arrow, three-role chain
  `analyst__adversary__architect` when an adversary phase
  participates. The flat `.ghyll/attestations.jsonl` continues
  as the aggregate tail (sum of all per-pass files).
- Insufficient-basis-rounds-max escalation flow: after the
  third `insufficient-basis` on the same clause, the modal
  presents a 2-option escalation (accepted-risk OR
  route-upstream) and the operator's choice drives the
  finding + clause status transitions.
- Oversized-residue-note rejection at the schema boundary
  (default 16KB, configurable per project).
- Minimal hint synthesis: the dispatcher constructs a hint
  payload from clause metadata (`{arrow_id, clause_id,
  concept, attestation_ref}`) when AwaitingAttestation
  flips on. No producer-side hook in Tier 2 (deferred to
  Tier 3 if richer hints are needed).

**Out of scope.**

- Producer-side richer hint payloads (locations / basis /
  residue). The scenarios' literal `{locations: ...}` text
  becomes operator-narrative; the runtime carries only
  clause-metadata-derived hints.
- Concurrent operators across multiple processes. ADR-006
  (one session per repo) excludes the "Alice + Bob both
  active simultaneously" scenario; Tier 2 only supports
  serial handoff (Alice ends → Bob starts).
- External operator UI (web / HTTP). All UX lives in the chat
  REPL.
- Distributed verdict-capture (the JSONL writes are local
  filesystem; ghyll-vault remains memory-checkpoint-only).

---

## Domain model

| Term | Definition |
|---|---|
| **Operator session** | An in-process record of the currently-active operator: `op_id` + `started_at` + `ended_at`. Bound to the chat-session lifetime. Handoff = ending one session and starting another in the same REPL. |
| **Verdict modal** | The chat REPL's interactive interruption when the dispatcher flips AwaitingAttestation. Blocks the turn until the operator submits a verdict OR explicitly defers ("`skip` — leave pending"). |
| **Verdict unit** | The shape of the operator's evidence. Three values: `confirm` (no payload), `record-locations-inspected` (requires `inspected` array of file:line refs), `write-residue-note` (requires `residue` string, max 16KB). |
| **Verdict record** | The persisted JSONL line. Carries: `attestation_id`, `kind`, `arrow_id`, `clause_id`, `op_id`, `attested_by_role`, `source_role`, `target_role`, `adversary_role` (gate-1 F-3), `context` (gate-1 F-2), `stratum` (gate-1 F-2), `pass_id` (gate-1 F-6), `verdict`, `unit`, `unit_payload_json` (per-unit JSON), `hint_json` (gate-1 F-25, default `'{}'`), `timestamp`, `grid_version`, `reason`. |
| **Hint** | The dispatcher-synthesized payload presented to the operator inside the modal. Minimal in Tier 2: `{arrow_id, clause_id, concept, attestation_ref}`. |
| **Escalation prompt** | The modal variant presented when `InsufficientBasisTracker` fires `OpEventInsufficientBasisRoundsExceeded`. Two options: `accepted-risk` (with residue note) OR `route-upstream` (with rationale). The operator must choose; no default. |
| **Tree JSONL** | Per-pass file at `<workdir>/.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`. One row per verdict. Path encoding: `__` between role segments. Init arrows use `init__analyst`; the role-pair `<context>` for init is `init` (project-scoped, not context-scoped). |
| **Aggregate JSONL** | The flat `<workdir>/.ghyll/attestations.jsonl` from Tier 1. Tier 2 keeps it as the tail-of-all-verdicts surface. The tree-writer is the load-bearing audit log; the aggregate is for `ghyll engine verify-attestations` and operator-wide audits. |

---

## Invariants

1. **Modal blocks the turn.** When the dispatcher's
   `AwaitingAttestation` fires, the chat REPL refuses to
   advance to the next model call until the operator submits
   a verdict OR explicitly defers (one of `pass` / `fail` /
   `insufficient-basis` / `skip`). The dispatcher's
   per-pass lock token stays held during the modal so a
   concurrent re-Dispatch cannot race.
2. **Tree JSONL is primary AND the boot loader.** Verdicts
   append to the per-pass tree file FIRST, with fsync,
   before the in-memory AttestationStore mutates. Tier 1's
   `PrimaryWriter` invariant (ErrAttestationAuditWriteFailed)
   lifts to the tree writer; the flat writer becomes an
   Observer. **At session start, AttestationStore.LoadFromTree
   reads the per-pass tree files** (NOT the flat aggregate);
   Recovery's `evaluationRunReconcile` works against the
   tree-populated in-memory store (F-1 / F-27 remediation).
   The flat aggregate is forward-only (written via Observer,
   never read at boot); `ghyll engine verify-attestations`
   walks both surfaces and reports any divergence with
   `ErrAttestationAggregateDivergence`.
3. **Unit-conditional schema is enforced at the write
   boundary.** A `record-locations-inspected` verdict with
   no `inspected` array rejects with
   `ErrVerdictUnitMissingField`; a `write-residue-note`
   verdict with a residue >16KB rejects with
   `ErrVerdictResidueTooLong`. Validation happens before any
   file or memory mutation.
4. **Multi-operator handoff is serial.** The session's
   `op_id` mutates ONLY via the `/op-id <new>` slash. A
   verdict-record carries the op-id active at submission
   time. Two records on the same pass with different op-ids
   reflect handoff; the tree JSONL preserves both lines in
   chronological order.
5. **Escalation prompt is final.** After 3 consecutive
   `insufficient-basis` verdicts (per
   `InsufficientBasisTracker`), the next modal presented is
   the escalation prompt — NOT another insufficient-basis
   verdict. The operator MUST choose option 1 or 2; no
   default, no skip. (Skip would leave the clause
   permanently pending.)
6. **Session-ends-mid-attestation preserves the request.**
   If the chat process exits while a modal is open, the
   request stays in `evaluation_runs.depth_type_attestation_ref`
   (Tier 1's persistent signal). On next session start, the
   chat REPL re-presents the modal at the first turn.
   `recovered_at` distinguishes preserved-via-Tier-1-recovery
   from fresh.
7. **Path encoding is deterministic and pure.**
   `EncodeAttestationPath(rec AttestationRecord) (path
   string, truncated bool, err error)` is a pure function
   of the record. AttestationRecord carries every field
   needed (GridVersion, Context, Stratum, SourceRole,
   AdversaryRole, TargetRole, PassID); no Grid lookup is
   required (F-2 remediation). The role_pair separator
   is `__`. Init arrows use the LITERAL "init" segment
   for role-pair AND context AND stratum (F-18
   remediation). A three-role chain (e.g.,
   `analyst→adversary→architect`) renders as
   `analyst__adversary__architect` iff `AdversaryRole`
   is populated on the record (F-3 remediation). Empty
   `PassID` rejects with `ErrAttestationPassIDEmpty`
   (F-6 remediation). Per-component byte cap of 255
   bytes; overflow returns `truncated=true` AND emits
   `ErrPathComponentTooLong` (F-17 remediation), but the
   write still proceeds with the hash-substituted segment.

---

## State machines (cross-reference)

The verdict modal introduces no new clause states beyond
Tier 1. Existing transitions still apply:

- `pass` verdict → `transitionClause(C, StatusPass)` via the
  AttestationStore primaryWriter chain.
- `fail` verdict → `transitionClause(C, StatusFail)`.
- `insufficient-basis` verdict → no clause-status change;
  increments `InsufficientBasisTracker.Record(arrow, clause,
  Insufficient)`.
- `accepted-risk` (escalation option 1) → the FINDING
  transitions to `accepted-risk`; the clause status updates
  via the existing `findings.go` derivation
  (`accepted-risk` is a terminal disposition).
- `route-upstream` (escalation option 2) → the arrow's pass
  aborts with reason `requires-deeper-artifact`; the
  producer role is re-dispatched at a deeper tier (next
  Dispatch cycle picks this up from the pass's abort
  reason).

---

## Behaviors (features)

### F-1: Session lifecycle

```gherkin
Scenario: Multi-operator handoff in one pass
  Given operator Alice is active and attests clause C1
  When Alice runs `/op-id bob@example.com` mid-chat
  Then the session's op_id changes to bob@example.com
  And Alice's previous record (op-id=alice) is unchanged
  And Bob's next verdict on C2 records op-id=bob
  And the pass's tree JSONL has both lines in chronological order

Scenario: Session ends mid-attestation
  Given Alice has a verdict modal open for clause C5
  And the chat process is killed (Ctrl-C)
  When the operator restarts the session
  Then the chat REPL detects evaluation_runs.depth_type_attestation_ref
      for C5 (Tier 1's persistent signal)
  And re-presents the verdict modal on the first turn
  And `recovered_at` is set on the pass (Tier 1's preserve flow)
```

### F-2: Verdict submission per unit

```gherkin
Scenario: Operator returns pass (unit confirm)
  Given an attestation request for clause C5
  And Alice is active
  When Alice submits verdict pass with unit "confirm"
  Then a record is appended to
      `.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`
  And the file is fsync'd before the dispatcher proceeds
  And the record carries: attestation_id, kind=depth-type,
      arrow_id, clause_id=C5, op_id=alice, verdict=pass,
      unit=confirm, timestamp (RFC3339Nano), grid_version
  And the flat `.ghyll/attestations.jsonl` ALSO receives the
      same record (fanout secondary)
  And C5's status transitions to pass

Scenario: Operator returns fail with record-locations
  Given an attestation request for clause C5
  When Alice submits verdict fail with unit
      "record-locations-inspected" and
      inspected=[features/contextA/payment.feature:42-50]
  Then the record's unit_payload JSON includes the inspected list
  And C5's status transitions to fail
  And the producer (clause's source role) is signaled via
      OpEventClauseFailVerdict for remediation

Scenario: Operator returns insufficient-basis with residue note
  Given an attestation request for clause C5
  When Alice submits verdict insufficient-basis with unit
      "write-residue-note" and
      residue="feature file too large for manual inspection"
  Then the record's unit_payload includes the residue string
  And the InsufficientBasisTracker increments to 1 for C5
  And C5's status STAYS Running (no flip — dispatcher will
      re-prompt on next traversal)
```

### F-3: Schema validation

```gherkin
Scenario: Missing required field is detected
  Given Alice attempts to submit verdict fail with unit
      "record-locations-inspected" but no inspected array
  Then the modal refuses with
      ErrVerdictUnitMissingField: "inspected"
  And no JSONL line is written
  And no in-memory mutation happens

Scenario: Oversized residue note rejected
  Given Alice attempts to submit verdict insufficient-basis
      with unit "write-residue-note" and residue=<16KB+1 bytes>
  Then the modal refuses with ErrVerdictResidueTooLong
  And the operator is re-prompted; no record appended
```

### F-4: Escalation after 3 rounds

```gherkin
Scenario: Three rounds, then escalation
  Given clause C5 has received insufficient-basis from rounds 1, 2
  And the dispatcher re-emitted the hint at a deeper depth tier each round
  When the next verdict on C5 is also insufficient-basis
  Then InsufficientBasisTracker fires
      OpEventInsufficientBasisRoundsExceeded
  And the chat REPL presents the escalation prompt with
      exactly 2 options:
        (1) accepted-risk with write-residue-note
        (2) route-upstream with rationale
  And neither is the default — operator must choose

Scenario: Operator accepts risk on the third round
  Given the escalation prompt is presented
  When Alice chooses option 1 with residue note
  Then a record is appended with verdict=accepted-risk,
      unit=write-residue-note, residue=<note>
  And the FINDING for C5 transitions to accepted-risk
  And C5's status transitions to pass IFF all findings on C5
      are disposed (resolved or accepted-risk)
  And the InsufficientBasisTracker round counter resets

Scenario: Operator routes upstream
  Given the escalation prompt is presented
  When Alice chooses option 2 with rationale
  Then the pass on the arrow aborts with reason
      "requires-deeper-artifact"
  And the producer role is re-dispatched at a deeper tier
      on the next Dispatch cycle
  And the abort record is in the tree JSONL with
      verdict=route-upstream, unit=write-residue-note,
      residue=<rationale>
```

### F-5: Path encoding

```gherkin
Scenario: Three-role chain path encoding
  Given an arrow with role chain analyst → adversary → architect
  When a verdict is recorded on a clause of that arrow
  Then the tree JSONL path component for the role-pair is
      "analyst__adversary__architect"
  And NOT "analyst-adversary-architect" or
      "analyst→adversary→architect"
  And the path is filesystem-portable (no Unicode glyphs,
      no path separators, ≤255 bytes per component)

Scenario: init arrow path encoding
  Given a verdict for an init arrow
  When the path is constructed
  Then the role-pair component is "init__analyst"
  And the context segment is the literal "init"
      (project-scoped, not context-scoped)
  And the path is `attestations/v<N>/init/stratum-<S>/init__analyst/<pass-id>.jsonl`
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Modal interrupted by Ctrl-C mid-prompt | Single verdict | Dispatch lock token stays held; the unfinished modal state is discarded. Next REPL start re-presents the modal (F-1 recovery flow). |
| FM-2 | Tree JSONL write fails (disk full, permission, fsync EIO) | Single verdict | Modal returns ErrAttestationAuditWriteFailed (per Tier 1 invariant 2). The in-memory store does not mutate; the operator is re-prompted. |
| FM-3 | Aggregate JSONL write fails after tree JSONL succeeds | Single verdict (audit-only) | Tree is authoritative (Tier 2 invariant 2). Aggregate divergence emits OpEventAttestationAuditDurabilityFailed. Reconciliation via `ghyll engine verify-attestations`. |
| FM-4 | Operator submits a verdict for a clause that has no pending request | Single verdict | Modal refuses with ErrNoPendingAttestation. The /attest CLI escape hatch (Tier 1) is the only way to record a verdict outside the modal flow. |
| FM-5 | Path-encoding produces a >255-byte path component | Single pass | EncodeAttestationPath returns ErrPathComponentTooLong. The fallback hashes the role-pair to a 16-byte hex digest prefix. Operator gets a typed event for audit. |
| FM-6 | Escalation prompt is presented but operator never answers (Ctrl-C) | Single clause | Dispatch lock stays held; clause stays pending; next REPL start re-presents the escalation prompt (NOT a fresh `insufficient-basis` modal). InsufficientBasisTracker round-count stays at the post-3-rounds state. |
| FM-7 | Producer role changes between rounds (e.g., adversary added mid-cycle) | Single pass | Path encoding uses the role-pair AT VERDICT TIME. Earlier rounds' tree JSONL stays at the old path; the new round's verdict lands at the new path. The aggregate JSONL has all lines. Operator-tooling can join across paths. |

---

## Cross-component interactions

- **Chat REPL ← dispatcher**. The dispatcher already publishes
  `OpEventAttestationRequested` (Tier 1 batch 2 / G2-F-11
  remediation). The chat REPL subscribes a new
  `attestationModalDriver` that consumes this event and
  presents the modal.
- **Modal ← AttestationStore**. The modal calls
  `AttestationStore.Record(rec)` with the constructed
  AttestationRecord; the primaryWriter chain (now the tree
  writer) persists.
- **Tree writer ← Aggregate writer**. Two paths today:
  (a) the existing flat `AttestationJSONLWriter` (Tier 1's
  primaryWriter). (b) A `AttestationTreeWriter` (built in
  phase 9, currently a secondary). Tier 2 swaps the roles:
  tree becomes primary, flat becomes observer fanout.
- **Modal ← InsufficientBasisTracker**. The tracker fires
  `OpEventInsufficientBasisRoundsExceeded` on the 3rd
  consecutive insufficient-basis. The modal driver
  subscribes; when fired, the next modal presented is the
  escalation prompt (not a vanilla verdict modal).
- **Modal → Producer-fix signal**. On verdict=fail, the modal
  publishes `OpEventClauseFailVerdict` so the dispatcher's
  per-arrow loop (or the future producer-fix harness) sees
  the signal.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | The chat REPL has an interrupt point between turns where modal presentation is safe. | Falsifies if the REPL turn loop is non-interruptible (a single streaming model call holds the goroutine for the entire turn). Verified against `cmd/ghyll/session.go`: the turn loop yields between user input and model call; insert the modal at the post-user, pre-model boundary. |
| A-2 | Operators can deal with a synchronous modal interruption. | Falsifies if operators are running long-tail model calls and don't want to be paused. Mitigation: the modal includes a `skip` option that defers the verdict; the clause stays pending; the dispatcher's per-pass lock token stays held; the operator can continue and answer later via the `/attest` CLI escape hatch. |
| A-3 | The tree path's role-pair separator (`__`) doesn't collide with any legitimate role name. | Falsifies if a role is ever named with `__` in it. Roles today are analyst/architect/implementer/integrator/adversary/init — none contains `__`. Operator-defined extended roles MUST avoid `__`; validate at init time. |
| A-4 | 16KB is enough headroom for residue notes. | Falsifies if a project's clause concept needs a >16KB residue context. The threshold is configurable per project (grid file's `residue-note-max-bytes`); default 16KB. |
| A-5 | The dispatcher's per-pass lock prevents re-Dispatch racing the modal. | Verified — `runner.RoleContextLockTable` holds the token for the full Dispatch call; modal presentation happens inside Dispatch. No race. |
| A-6 | One operator at a time per repo (ADR-006). | Tier 2 maintains this. The "two operators submit verdicts on the same clause near-simultaneously" scenario is genuinely deferred to Tier 3+ (multi-machine support). |

---

## Open questions (for architect)

1. **Modal contract**. The chat REPL today is a turn loop in
   `cmd/ghyll/session.go`. Architect: do we introduce a
   separate `OperatorModalPrompt` interface (so tests can
   stub modal answers) or inline the prompt logic in the
   REPL? Recommend a separate interface for testability;
   the BDD layer needs to drive verdicts programmatically.
2. **Tree-writer-as-primary inversion blast radius**.
   Today's `AttestationStore.SetPrimaryWriter(jsonlWriter.PrimaryWriter())`
   in `session_engine.attachJournal` (Tier 1, line 366ff)
   wires the FLAT writer as primary. Tier 2 swaps this for
   the tree writer. The flat writer becomes a regular
   Observer. Architect: confirm the swap doesn't break the
   `recovery.evaluationRunReconcile` flow which consults
   the in-memory `AttestationStore.Lookup` (it doesn't —
   Lookup is via byID, not writer-mediated).
3. **Hint payload future**. Tier 2 ships with a minimal
   clause-metadata hint. Operators who want richer hints
   (locations, basis, residue) need Tier 3's producer-hint
   hook. Architect: should the AttestationRecord schema
   already include a `hint_json` field so Tier 3 doesn't
   need a schema migration? Recommend yes (`hint_json TEXT
   NOT NULL DEFAULT ''`); Tier 2 writes the minimal hint;
   Tier 3 populates it from the producer.
4. **`/attest` CLI vs modal**. The Tier 1 `/attest` slash
   command remains as an escape hatch. Architect: should it
   be deprecated (operators can only use the modal) or
   stay as a power-user surface? Recommend STAY — the modal
   is the primary path; `/attest` is for replay-from-script
   workflows, BDD testing, and operators who prefer typing.
5. **Multi-pass aggregation in the tree**. A pass can have
   multiple verdicts (one per clause). The tree JSONL is
   per-pass-id, so all clauses on one pass append to the
   same file. Architect: confirm this matches the scenario
   text ("the attestation file for the pass records ...
   verdicts on C2, C3 within the same pass" — F-1). Yes.

---

## Scenarios this spec covers

When implemented, these 12 of 15 attestation.feature
`@deferred` scenarios lift:

- Multi-operator handoff in one pass
- Operator returns pass (with unit confirm)
- Operator returns fail with record-locations
- Operator returns insufficient-basis with residue note
- Three rounds, then escalation
- Operator accepts risk on the third round
- Operator routes upstream
- Missing required field is detected
- Oversized residue note rejected
- Three-role chain path encoding
- init arrow path encoding
- Operator's session ends mid-attestation

**Genuinely blocked** (Tier 3+):

- Two operators submit verdicts on the same clause
  near-simultaneously (multi-machine; ADR-006 conflict).
- Invalid insufficient-basis-rounds-max — non-integer
  (deferred YAML loader, separate concern).
- op-id rejection — meta-described inputs (programmatic
  test fixture concern; the `ValidateOpID` unit tests
  already cover the runtime checks).

= 12 of the remaining 36 `@deferred` scenarios across the
suite.

---

## Handoff to architect

Architect picks up:

1. **ADR for the tree-writer-as-primary swap** (amends
   ADR-015 Part C or supersedes with ADR-016). The flat
   writer remains as an observer for the aggregate audit
   trail.
2. **OperatorModalPrompt interface**: shape, where it lives
   (cmd/ghyll? new ui/modal package?), and how it bridges
   to the AttestationStore.Record call.
3. **AttestationRecord schema extension**: new fields
   `unit`, `unit_payload_json`, optional `hint_json` —
   schema migration (schemaVersion 3 → 4).
4. **Modal driver** in cmd/ghyll/session.go: subscribes
   `OpEventAttestationRequested`, presents the modal at the
   next REPL turn boundary, calls `AttestationStore.Record`,
   handles the escalation-prompt variant on
   `OpEventInsufficientBasisRoundsExceeded`.
5. **Path encoding helper**: `EncodeAttestationPath(grid_version,
   context, stratum, role_pair, pass_id) (string, error)`.
   Validates the 255-byte limit; falls back to a hash
   prefix on overflow.
6. **Verdict-unit schema**: `VerdictUnit` enum + per-unit
   payload struct + `ValidateUnitPayload(u, payload) error`.
