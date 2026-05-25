# Glossary

The gate-and-arrow runtime introduces a small vocabulary that the
rest of the docs lean on heavily. If a term feels obvious in
context but you want a worked example, this is the page to bookmark.

Each entry has the same three pieces: a bare-minimum definition,
one worked example, and where you will see the term in the CLI or
on disk.

## Arrow

A typed, persisted declaration that one role hands work off to
another role inside a bounded context. The arrow names the source
role, the target role, the context, the stratum (depth tier), and
the list of [clauses](#clause) the runtime must evaluate before
the transition is legal.

Worked example: `A-analyst-architect-default` declares that the
**analyst** hands off to the **architect** inside the **default**
context. Until every clause attached to this arrow reaches a
[verdict](#verdict), the arrow stays open and no follow-on work
runs.

Where you see it:

- On disk: `.ghyll/grid.v1.yaml` (after `ghyll init`).
- In the CLI: `/list-arrows` shows the full set;
  `ghyll arrow show A-analyst-architect-default` renders one
  arrow's live state.

## Pass

One runtime invocation of an arrow: open the arrow, run its
clauses, collect verdicts, close. Multiple passes can run against
the same arrow over a project's life — each pass is a fresh
attempt to drive that arrow to `closed:ok`.

Worked example: when you run `/run-arrow A-analyst-architect-default`
the runtime opens a pass (call it `p-7`), evaluates clauses one
at a time, possibly opens a verdict modal for attested clauses,
then closes the pass with `closed:ok` or `closed:failed`.

Where you see it:

- In the REPL: `· pass-opened   pass=p-7 role=analyst ...` and
  `· pass-closed   pass=p-7 ... state/reason=closed:ok`.
- In the CLI: `/passes` lists open passes; `/passes <pass-id>`
  shows one pass's full state.
- On disk: the per-pass attestation tree at
  `.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.

## Clause

A single typed gate condition attached to an arrow. Every clause
has two type tags — an evaluation type (`machine` or `attested`)
and a depth type (`depth-robust` or `depth-sensitive`) — that
together decide whether the runtime can evaluate it on its own
and which tier should run.

Worked example: `lint-clean` is `machine` / `depth-robust` — the
runtime just runs the linter. `acyclic-dependency-graph` is
`attested` / `depth-sensitive` — the deep tier drafts the
analysis and the operator confirms via a verdict modal.

Where you see it:

- On disk: the `clauses:` list under each arrow in
  `.ghyll/grid.v1.yaml`.
- In the CLI: `ghyll arrow show <id>` lists every clause with
  its id, depth tag, and min-tier.

## Verdict

The four-valued outcome an operator (or the runtime, for machine
clauses) records against a clause: `pass`, `fail`,
`insufficient-basis`, or `skip`.

Worked example:

- **pass**: I looked at the work, it meets the clause's contract.
- **fail**: I looked, it does not — record what I inspected.
- **insufficient-basis**: I cannot evaluate from what I have —
  record a [residue](#residue) note describing what's missing.
- **skip**: punt to the next round (the same prompt re-presents).

Three consecutive `insufficient-basis` verdicts on the same
clause fire the escalation prompt described in the
[operator guide](operator-guide.md#verdict-modal-tier-2--adr-016).

Where you see it:

- In the REPL: `verdict? [pass / fail / insufficient-basis / skip]:`
  prompts inside the Tier 2 modal.
- On disk: the `verdict` field of each line in
  `.ghyll/attestations.jsonl`.

## Role

One of the four fixed roles in the diamond — **analyst**,
**architect**, **implementer**, **integrator**. The set is fixed
at build time per [ADR-008](decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md);
you cannot add a `reviewer` or `tester` at runtime, and you
cannot collapse two roles into one.

Worked example: the analyst hands off to the architect across
arrow `A-analyst-architect-default`. The architect's exit-gate
clauses on that arrow are what the operator (or the runtime)
will evaluate before the next role gets to act.

Where you see it:

- On disk: `specs/architecture/roles/{analyst,architect,implementer,integrator}.md`
  in the repo; the running binary uses an embedded copy.
- In the CLI: `source-role:` / `target-role:` lines in
  `ghyll arrow show`.

## Context

In ghyll's v2 docs, "context" means a **bounded context** in the
DDD sense — a logical scope of the project, like `checkout` or
`inventory`. This is NOT the LLM context window. The LLM-window
meaning still applies inside `docs/internals/context.md`, but
everywhere else "context" is the DDD term.

Worked example: a single repo can have arrows in two contexts,
`checkout` and `inventory`. The arrows are independent — the
analyst→architect handoff on `checkout` does not block the same
handoff on `inventory`.

Where you see it:

- On disk: `contexts:` block at the top of `.ghyll/grid.v1.yaml`.
- In the CLI: `context=...` column in `/list-arrows`; the
  `--context` flag of `/run-arrow`.

## Stratum

Which tier the pass runs at — gate-driven, NOT model-chosen. The
arrow's clauses set the maximum depth requirement; the dispatcher
picks the lowest tier that meets it. The model never votes on its
own stratum.

Worked example: an arrow whose clauses are all `depth-robust` has
`stratum=L0` and runs on MiniMax M2.5. An arrow with one
`depth-sensitive` clause has `stratum=L1` or higher and runs on
GLM-5.

Where you see it:

- In the CLI: `stratum=L1` column in `/list-arrows`; `stratum:`
  line in `ghyll arrow show`.
- On disk: the `stratum:` field on each arrow in
  `.ghyll/grid.v1.yaml`.

## Op-id

An email-like string identifying the human at the keyboard. You
set it once per session with `/op-id you@example.com` (or
implicitly at `ghyll init --op-id ...`). Every
[attestation](#attestation) you record is tagged with this op-id.

Two operators on the same project MUST have different op-ids —
the [self-cert](#self-cert) rule (§12.2) means a shared op-id
breaks the cross-check. The CLI rejects op-ids with control
bytes, separators, ".." substrings, Unicode-format runes (RTL
override, ZWSP, ZWJ, BOM), a leading dot or dash, a trailing
dot, or > 256 bytes (`validateOpID`).

Where you see it:

- In the REPL: `/op-id alice@example.com` to set, `/op-id` to
  show, `/op-id clear` or `/op-id none` to clear.
- On disk: the `op_id` field of each line in
  `.ghyll/attestations.jsonl`.

## Residue

The note an operator writes when they pick `insufficient-basis`
on a clause. A good residue note names the artifact you wanted
to inspect and didn't have; it's what tells a future operator
(or your future self) what would change the verdict.

Worked example: "needs the analyst's threat-model doc for the
checkout flow; not present in this branch yet" — that's a
residue note. The grid's `residue-note-max-bytes` caps the field.

Where you see it:

- In the REPL: the modal prompts for the note inline when you
  pick `insufficient-basis`.
- On disk: the `residue_note` field of the attestation record;
  also bootstrap-time residue lines in `.ghyll/grid.v1.yaml`
  under `residue:` (auto-skipped clauses leave a machine-parseable
  reason there).

## Modify rule

The auto-propose rewriting layer used during `ghyll init`. When
the bootstrap builds a clause proposal, modify rules can rewrite
path-glob and regex arguments to fit what the project actually
looks like (e.g. narrowing `src/**` to `src/main.go` when the
narrower form is provably a subset).

Worked example: a role-clause declares `tests-pass(./...)` and
the modify layer can refine the args directionally — never the
other way around. The intent is "make the proposed clause more
specific to this project", not "let the runtime invent new ones".

Where you see it:

- On disk: rules live in the embedded role contracts under
  `specs/architecture/roles/`; their effects show up as the args
  recorded against clauses in `.ghyll/grid.v1.yaml`.

## Amendment

Grid evolution via the integrator role: a new bounded context
appears, an arrow needs a clause added, a residue note is being
resolved. Amendments serialize through a global lock, abort any
open passes on the affected arrow, append the new arrow
definitions, and bump the grid version (`grid.v2.yaml`,
`grid.v3.yaml`, ...). The CLI for triggering an amendment
manually is not yet exposed — today it is runtime-driven by the
integrator.

Where you see it:

- On disk: each new grid version writes a new
  `.ghyll/grid.v<N>.yaml`; `grid.current` points at the active
  one.
- In the CLI: `ghyll engine status` shows
  `amendments: <pending> pending, <drained>` counts.

## Adversarial pass

A scheduled R0 attack against the current evidence on an arrow.
A fresh adversary raises findings, the original producer is
asked to fix them, and the cycle loops. ghyll fingerprints the
producer's output each round; two rounds with identical output
and unresolved findings is the **loop-bomb** signal —
`ErrProducerLoopBomb` — and the cycle aborts so the operator
can intervene.

Worked example: the adversary surfaces "the analyst's spec
omits the failure mode for partial payments"; the producer
revises the spec; the adversary re-attacks. When the producer
plateaus, you see `producer-fix-signal` events and eventually a
prompt to break the deadlock.

Where you see it:

- In the REPL: `producer-fix-signal` events inline; the
  deadlock prompt when the loop-bomb fires.
- On disk: findings raised during the cycle are persisted in
  `.ghyll/engine.db` (see [finding](#finding)).

## Finding

A remediation-tracked issue raised during a pass — typically by
the adversarial cycle, sometimes by a machine clause that
returned actionable output. Findings have severity, are tied to
an arrow, and stay open until they reach `closed:remediated`,
`closed:accepted-risk`, or `closed:wont-fix`.

Worked example: the adversary's "this spec omits the partial-
payment failure mode" turns into a finding on
`A-analyst-architect-default` that the analyst must address
before the arrow can close.

Where you see it:

- On disk: persisted to `.ghyll/engine.db`.
- In the CLI: `findings:` block of `ghyll arrow show`;
  `findings:` counter of `ghyll engine status`.

## Attestation

The JSONL record an operator's verdict produces. Each attestation
captures the arrow, the clause (for depth-type attestations), the
verdict, the op-id, an optional residue or rationale, and a
timestamp. Attestations are **immutable** — to correct a verdict,
you record a new attestation on a later pass.

Worked example: `att-A-checkout-C1-v1  kind=depth-type  clause=C1  verdict=pass  op=alice`
is one line of `ghyll arrow show A-checkout`'s output.

Where you see it:

- On disk: flat audit at `.ghyll/attestations.jsonl` AND the
  per-pass tree at
  `.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.
- In the CLI: `/attest`, `/attestations`, and `ghyll arrow show`
  all render attestations; `ghyll engine verify-attestations`
  audits the JSONL.

## Self-cert

The §12.2 invariant: you cannot attest your own work. If your
op-id is recorded as the actor on an arrow's source or target
role, that same op-id cannot serve as the attesting authority
on the arrow's clauses. The runtime AttestationStore and the
engine schema both enforce this; `ghyll engine verify-attestations`
also detects self-cert in the on-disk JSONL.

Worked example: alice played the analyst on
`A-analyst-architect-default`. When she later tries to attest a
clause on that arrow, the runtime rejects the record — someone
with a different op-id (typically the architect or a reviewer)
must verify instead.

Where you see it:

- In the CLI: rejection error from `/attest` when the op-id /
  role pair would violate self-cert.
- On disk: `ghyll engine verify-attestations` flags any tampered
  JSONL line that bypasses the runtime check.

## Embedded data

Data baked into the binary at build time via Go's `//go:embed`.
ghyll embeds the four role contracts (RolesFS), the closed
concept catalogue (ConceptsFS), and the default config template
(`config/example.toml`). You do NOT need a copy of these files
on disk for a fresh install to work — `ghyll init` reads them
from the binary.

Worked example: a freshly-built `ghyll` binary moved to a
machine with no source tree can still run `ghyll init` and
produce a complete grid, because the role files and concept
schemas it needs are embedded.

Where you see it:

- In the source: `//go:embed specs/architecture/roles/*.md` and
  `//go:embed gates/concepts/*.yaml`.
- In the CLI: `ghyll run` on first start writes
  `~/.ghyll/config.toml` from the embedded template and exits;
  `ghyll init` reads its role contracts and concept catalogue
  from the embedded data.

---

See also: [Why ghyll](why.md) for the design rationale,
[Getting Started](usage/getting-started.md) for the first-session
walkthrough, and the [Operator Guide](operator-guide.md) for the
deep CLI reference.
