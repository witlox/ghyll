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
(handing off) and the target role (receiving). The definer hook
(an LLM-backed function in `runner/onthespot.go::safeInvokeDefiner`)
authors the new `ArrowDefinition` in isolation; the source and
target roles do not write the clauses or arguments. They are
*stakeholders*, not authors:

- The source role is about to hand off under the new contract — it
  benefits if the contract is easy to produce.
- The target role is about to receive under the new contract — it
  benefits if the contract is easy to consume.

The runner code at `runner/onthespot.go:178-189` currently forbids
both source and target role from attesting, flagging this as
"conservative reading (F5)" and recommending an analyst pass to pin
the spec.

## Decision

§12.2 forbids attestation by both the source role and the target
role of an on-the-spot arrow definition. The conservative runner
reading is the correct reading.

## Rationale

The two roles are **stakeholders with direct conflict-of-interest**,
not authors of the definition. A stakeholder attesting their own
beneficial contract is the same failure mode as a producer attesting
their own work: the gate becomes negotiable. The intent of §12.2 is
that the operator attestation be an external check; allowing either
stakeholder to attest leaves a private-vote channel where the
beneficiary decides whether the contract is acceptable.

Symmetry across the two endpoints is also simpler to enforce and
reason about than asymmetric rules.

### What the check enforces

The runtime check is a syntactic gate at the schema boundary:
`att.AttestedByRole` must not equal source or target role strings.
The check does not verify that the operator presenting the
attestation actually holds the claimed role — that's outside §12.2
scope and is the responsibility of the operator-identity layer
(authentication, audit log). §12.2 enforces the structural
constraint: "whoever attests, the recorded `AttestedByRole` is
neither endpoint."

### Who can attest

The attester must be a role that is not the source or target of the
arrow being defined. In the four-role diamond
(`analyst → architect → implementer → integrator`):

- For an `analyst → architect` on-the-spot arrow: `implementer` or
  `integrator` may attest.
- For an `architect → implementer` arrow: `analyst` or `integrator`
  may attest.
- For an `implementer → integrator` arrow: `analyst` or `architect`
  may attest.

If no operator can attest under a non-endpoint role (e.g., a
configuration where the diamond is reduced to two roles), the
on-the-spot definition cannot proceed. The runner surfaces this as
`ErrSelfCertImpossible`. The operator must amend the workflow (e.g.,
declare a third role) before retry.

## Consequences

### Schema update

`specs/architecture/gates.md` §12.2 paragraph 2 reads:

> This definition is itself gated by operator attestation — neither
> the source role nor the target role may attest the definition,
> because both have conflict-of-interest as immediate stakeholders
> in the contract (ADR-009). The attestation must come from a role
> outside the (source, target) pair. If no such role exists in the
> current workflow, the on-the-spot definition fails with
> `ErrSelfCertImpossible` and the operator must amend the workflow
> before retry.

### Code

- `runner.ResolveOnTheSpot` already enforces the role check.
- Add sentinel `ErrSelfCertImpossible` for the no-eligible-role
  case. The runner surfaces this when an operator presents an
  attestation that fails the role check AND the workflow has no
  other roles to attempt.
- `AttestationStore.Record` (per ADR-010) also enforces the
  role check at the persistence boundary, so a buggy or
  out-of-band recording path cannot bypass §12.2.

### Test

`runner/onthespot_test.go:160` already covers the target-role-cannot-
attest case (`source self-cert should be refused`,
`target self-cert should be refused`, case-insensitive variant).
Adds: a test asserting `ErrSelfCertImpossible` is returned when no
eligible role exists in the operator-presented attestation chain.

## Alternatives considered

1. **Source role only.** Narrower reading of "producing". Rejected:
   target-role self-cert leaves the beneficiary deciding whether the
   contract is acceptable, defeating the gate.
2. **Either role + any other operator with conflict-of-interest.**
   Generalized; harder to enforce because "conflict-of-interest"
   isn't statically declarable in the schema. Rejected for v1.0.0;
   a future ADR can extend.
3. **Allow target role IFF the operator's role audit log shows the
   target role didn't participate in authorship.** Rejected:
   authorship is owned by the definer hook, not by either role, so
   the audit log can't distinguish. The structural rule is simpler.

## Related

- `specs/architecture/gates.md` §5.4, §12.2
- `runner/onthespot.go` `ResolveOnTheSpot`
- `specs/invariants.md` inv 27 (producers cannot self-certify)
- `specs/failure-modes.md` FM-49 (producer attempts self-attestation)
- ADR-010 (AttestationStore enforces the constraint at the
  persistence boundary)
