# ADR-v4-005: OperatorEvent typed Payload contract — `outcome` / `reason` key split

**Status:** Accepted (2026-05-25)

**Context:**

`OperatorEvent.Payload map[string]string` is the typed-payload
surface for subscribers (modal driver, status CLI, JSONL audit
writer). The free-text `Detail` field is for human reading; v1 had
subscribers parsing `Detail` for state, which is fragile.

The v1 contract used `outcome` / `status` / `reason` keys
inconsistently across event kinds. R20 of the v2 adversarial flagged
this.

**Decision:**

Unified contract:

- **`outcome`** is a closed enum: the terminal state of the event's
  subject. Subscribers `switch` on this. Values per event kind are
  enumerated in `specs/v4/diamond-load-bearing-revised-v2.md`
  "OperatorBus payload contracts".
- **`reason`** is free-text: a one-line human-readable
  amplification. Subscribers display this; they do NOT switch on
  it.

Event kinds with required Payload keys (summary; full table in the
spec):

| Event kind | Required Payload keys |
|---|---|
| `OpEventAdversarialRoundStart` | `arrow_id`, `pass_id`, `round`, `rounds_max`, `open_findings`, `tier_label` |
| `OpEventRemediationConverged` | `arrow_id`, `pass_id`, `outcome` ∈ {`converged`, `converged-with-unevaluated`}, `rounds_used` |
| `OpEventRemediationEscalated` | `arrow_id`, `pass_id`, `outcome` ∈ {`escalated-rounds-max`, `escalated-no-progress`, `escalated-hook-error`, `context-cancelled`}, `rounds_used`, `reason` |
| `OpEventAmendmentEnqueued` | `arrow_id`, `amendment_id`, `source_arrow`, `target_role`, `finding_ids` |
| `OpEventAmendmentEnqueueRefused` | `arrow_id`, `amendment_id`, `outcome` ∈ {`queue-full`, `duplicate-id`} |
| `OpEventAmendmentDrained` | `amendment_id`, `source_arrow`, `grid_version_before`, `grid_version_after`, `arrows_added`, `passes_aborted`, `outcome` ∈ {`complete`, `partial-append-error`, `binding-re-register-error`} |
| `OpEventRecoveryAmendmentsPending` | `count`, `amendment_ids` |
| `OpEventArrowInvalidated` | `arrow_id`, `op_id`, `reason`, `timestamp` |
| `OpEventPassClosed` | `arrow_id`, `pass_id`, `close_reason`, `arrow_status` ∈ {`complete`, `unevaluated`, `blocked`, `aborted-remediation`} |

**Consequences:**

- A single unit test (`TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency`)
  enforces the contract across event kinds.
- The modal driver and status CLI become simpler: a Go `switch` on
  `Payload["outcome"]` covers every terminal state.
- Adding a new closed-enum value to `outcome` is an additive
  change; subscribers that don't recognize it fall through to a
  default branch and surface `reason` to the operator.
