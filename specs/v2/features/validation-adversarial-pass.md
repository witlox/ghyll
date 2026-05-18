# Validation — adversarial pass on v2 features

Cold-context attack on `specs/v2/features/*.feature` on 2026-05-18.
Mirrored the schema's own per-arrow adversarial phase (three
sub-activities: clause-falsification, open sweep, depth
classification). The features had been extracted from
component-design markdown which emphasized primary behavior — the
adversarial pass confirms they need failure-path additions before
serving as the executable contract.

**Verdict.** Not ready to wire to step definitions. Structural
coverage is good (every component has a feature file, every major
flow has a happy path), but assertions consistently stop at
"a record was appended" / "status was set" / "the transition was
rejected (this one)." A naive in-memory stub clears ~60% of the
suite. Adding ~25–40 scenarios and tightening assertions on
~10 existing ones closes the gap.

## Findings — hardest first

| # | Tag | Target | Severity | Basis |
|---|---|---|---|---|
| 1 | clause-falsification | `runner.feature: Successful machine evaluation` | critical | Asserts evaluator "runs to completion" + record appended; never asserts the artifact was actually scanned or "no TODO" determined from real content. A stub returning `{pass:true, hits:[]}` passes. |
| 2 | clause-falsification | `state-machine.feature: Invalid clause transition rejected` | critical | Only checks one illegal transition (`pass→pending`). The state machine has many illegal transitions; a stub that hard-codes a single deny passes. The illegal-transition matrix is untested. |
| 3 | open-sweep | missing from `amendment.feature` | critical | No scenario for lock-liveness / deadlock. An amendment holding the lock while waiting on attestation flow which waits on a clause held by an aborted pass — the whole "global write-lock" claim is untested for liveness. |
| 4 | clause-falsification | `attestation.feature: Operator returns pass` | high | Asserts record "carries unit/clause/verdict/ts/op-id"; doesn't assert path canonicalization for `__`-separated three-role chains, JSONL append atomicity, fsync. A buggy impl that writes via `os.WriteFile` (truncating) passes. |
| 5 | open-sweep | missing from `attestation.feature` | high | Adversarial input on `op-id`: no scenario for op-id containing `/`, `..`, `\n`, NUL, 4KB string, unicode RTL override, shell metachars. If op-id leaks into any path, directory traversal. |
| 6 | depth-classification: SHALLOW | `runner.feature: Two passes on different contexts run concurrently` | high | "Neither's evaluation runs interfere with the other's" is unfalsifiable without a real concurrency probe. A serial implementation passes. |
| 7 | open-sweep | missing from `state-machine.feature` | high | Crash recovery only covers `running → aborted: crash`. Not tested: crash mid-`awaiting-attestation` (verdict-in-flight loss), crash between attestation write and status flip (split-brain), checkpoint log truncated last record. |
| 8 | clause-falsification | `adversarial.feature: Producer fixes a finding with full re-attack` | high | Asserts R1 "receives same inputs as R0 except updated artifact"; doesn't assert R1 has clean context (no leakage from R0), R1's depth tier matches R0's, R1 actually re-runs all three sub-activities. Anti-collusion guarantee unverified. |
| 9 | open-sweep | missing from `init.feature` | high | No scenario for `grid.current` pointing at `vN` while `grid.vN.yaml` is absent / unreadable (post-rollback, partial restore, manual edit). Pointer indirection has no failure scenario beyond mid-write crash. |
| 10 | clause-falsification | `amendment.feature: Successful atomic write of v(N+1)` | high | Checks rename order but not that a reader observing `grid.current=vN+1` also sees the new file on disk (fsync of directory entry). On ext4 default, rename can be visible before file content is durable. |
| 11 | depth-classification: MOCKED | `adversarial.feature: Adversary tier too shallow to classify` | medium | Depth-sensitivity requirement is the unit under test but no scenario binds a concrete tier value or shows the gate computation. Implementation can always return `unevaluated` and pass. |
| 12 | open-sweep | missing from `runner.feature` | medium | No scenario for evaluator process: timeout, OOM-kill, exits 0 with malformed JSON on stdout, writes to stderr, leaves zombie children, returns 2GB of `details`. |
| 13 | open-sweep | missing from `adversarial.feature` | medium | `remediation-rounds-max` boundary: round=max-1, =max, =max+1; what if producer signals "fix" but artifact unchanged (loop bomb)? |
| 14 | clause-falsification | `attestation.feature: insufficient-basis-rounds-max is configurable` | medium | Only asserts round 4 doesn't escalate at max=5. Doesn't assert round 5 *does* escalate, or that max=0/1/negative is rejected at init. |
| 15 | open-sweep | missing from `state-machine.feature` | medium | Residue computation: what if a role's exit-gate template references a binding the project hasn't declared (cost undefined)? Non-integer / negative / overflow costs? |
| 16 | depth-classification: SHALLOW | `amendment.feature: Conservative fallback for no-deps arrows` | medium | Asserts abort but not that operator tooling actually surfaces "should have declared dependencies"; warning channel is hand-waved. |
| 17 | open-sweep | missing from `init.feature` | medium | "Modify raise-only" tested for one numeric threshold; not tested for non-monotonic args (regex strings, scope globs, enum tightening), or modify on a non-existent field. |
| 18 | clause-falsification | `runner.feature: Invalidated arrow refuses transitions` | low | Asserts refusal kind only; no assertion that the "needs re-traversal" signal reaches anything observable. |

## Top-5 missing scenario categories

1. **Evaluator/process failure modes** — timeout, OOM, malformed
   output, stderr noise, zombies. Affects runner + adversarial.
2. **Crash recovery between component boundaries** — attestation
   written / status not yet flipped; checkpoint truncated; grid.current
   points at missing version.
3. **Adversarial operator input** — op-id traversal/control chars,
   malformed verdicts, JSONL injection, oversized residue notes.
4. **Concurrency liveness & ordering** — lock acquisition order, FIFO
   under contention, abort-during-attestation races,
   single-active-role under crash.
5. **State-machine illegal-transition matrix** — currently one example
   stands in for an entire negative space; same for finding lifecycle
   (`accepted-risk → ?`, `resolved → open`).

## Path forward

Additions pass: ~25–30 new scenarios across the six files,
addressing the categories above. Tighten ~10 weakly-asserted
existing scenarios (the critical/high items 1–10).

Done in the same commit as this findings doc.
