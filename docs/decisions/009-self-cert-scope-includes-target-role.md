# ADR-009: §12.2 self-cert scope includes target role

Date: 2026-05-19
Status: Accepted

## Context

`specs/architecture/gates.md` §12.2 reads:

> the producing role may not self-certify the definition it wants to
> use to continue.

The natural reading of "producing role" is the role producing the
artifact — i.e., the **source role** of the transition. On-the-spot
arrow definitions, however, have two endpoints: the source role
(handing off) and the target role (receiving). The arrow definition
records the contract between them; both roles have visibility into
its content as it is being authored.

The runner code at `runner/onthespot.go` (lines 178–189) currently
forbids both source and target role from attesting, flagging this as
"conservative reading (F5)" and recommending an analyst pass to pin
the spec.

## Decision

§12.2 forbids attestation by both the source role and the target
role of an on-the-spot arrow definition. The conservative runner
reading is the correct reading.

## Rationale

On-the-spot arrow creation is a joint act between two roles. The
source role is about to produce an artifact under the new contract;
the target role is about to consume one. Both roles co-author the
definition under load — the source declares what it can produce,
the target declares what it can accept. An operator attestation
from either role is a vote in a contract the role has already
shaped.

The spec's intent is that the operator attestation be a check
external to the definition-authoring loop. Allowing the target role
to attest leaves a self-cert channel: a target role that wants a
weak contract for its own consumption can author and then ratify
that weakness.

Symmetry across the two endpoints is also simpler to enforce and
reason about than asymmetric rules.

## Consequences

### Schema update

`specs/architecture/gates.md` §12.2 paragraph 2 is updated to read:

> This definition is itself gated by operator attestation — neither
> the source role nor the target role may attest the definition,
> because both have conflict-of-interest as co-authors of the
> contract.

### Code

No code change: `runner.ResolveOnTheSpot` already enforces this. The
"F5 / conservative reading — escalate to analyst" comment becomes
"per ADR-009". The escalation marker can be dropped.

### Test

`runner/onthespot_test.go:160` already covers the target-role-cannot-
attest case (`source self-cert should be refused`,
`target self-cert should be refused`, case-insensitive variant). No
new tests needed.

## Alternatives considered

1. **Source role only.** Narrower reading of "producing". Rejected:
   target-role self-cert leaves an authoring/ratification loop in
   the same hands, defeating the gate.
2. **Either role + any other operator with conflict-of-interest.**
   Generalized; harder to enforce because "conflict-of-interest"
   isn't statically declarable in the schema. Rejected for v1.0.0;
   a future ADR can extend.

## Related

- `specs/architecture/gates.md` §5.4, §12.2
- `runner/onthespot.go` `ResolveOnTheSpot`
- `specs/invariants.md` inv 27 (producers cannot self-certify)
- `specs/failure-modes.md` FM-49 (producer attempts self-attestation)
