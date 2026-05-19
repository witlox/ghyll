# ADR-001: The v2 pivot — correctness over speed and breadth

**Status:** Accepted (2026-05-18)

**Context:**

v1 ghyll positioned itself as "Claude Code for self-hosted models" —
a delivery-positioned agent with dialects, drift-aware memory, and
streaming as differentiators. The motivating evidence for a pivot
came from an artifact-level audit of a prior project ("Kiseki")
where work reported complete proved substantially incomplete on
cloud deployment: ~17 of 40 requirements specified-but-shallow
(tests that ran the code but asserted almost nothing); 23 operations
claimed "THOROUGH" by the verification role with zero tests. The
root cause was prose-only role definitions with no structural
enforcement — definitions drifted because nothing held them.

The 2026 market is moving toward more agent autonomy (longer
unsupervised loops, parallel-agent throughput). v2 moves the
opposite direction: constrained autonomy, mandatory gates,
operator-in-the-loop, explicit refusal of low-risk projects.

**Decision:**

v2's differentiator is **behavioral, not infrastructural**. ghyll
optimizes for **correctness over speed and breadth**, pays in
**friction**. v2 is:

- Correct for a narrow class of work: novel architecture,
  correctness-critical systems, long-horizon projects where a
  defect reaching deployment is expensive.
- Wrong for CRUD, migrations, glue code, rapid prototyping — where
  throughput is the win and ghyll's friction is pure cost.

The schema enforces a typed gate system (clauses, arrows, passes,
findings) rather than relying on prose definitions. v1 code stays as
**continuity infrastructure** (dialects, memory, streaming) but is
no longer the differentiator.

**Consequences:**

- **Rules in:** an opinionated workflow (the diamond); structural
  refusal of transitions when arrows don't close; explicit residue
  reporting; init refusal for projects where ghyll is wrong.
- **Rules out:** throughput-optimized parallel-agent execution;
  silent skipping of checks; "complete" as a binary; one-tool-for-everything
  positioning.
- **Risk:** the operator-in-the-loop premise constrains scale.
  Single-process per project; single-operator default (multi-operator
  supported but not optimized). Distributed ghyll is out of scope
  for v1.
- **Honesty cost:** ghyll must be able to say "use a fast agent
  instead" at init time. If refusal acceptance rate is near 0 in
  practice, refusal becomes dead friction and the differentiator
  weakens.

See `specs/architecture/direction.md` for the full pivot rationale.
