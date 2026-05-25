# ADR-v4-002: Adversarial phase auto-enabled on dialect availability

**Status:** Accepted (2026-05-25)

**Context:**

The dispatcher's §11 adversarial phase requires three LLM-backed
hooks (open-sweep, depth-classify, producer-fix) plus an Adversary
factory. v1 reached production with the hooks reachable only from
tests; the dispatcher never instantiated them. The v4 diamond pass
wires them, but the default behavior matters: auto-enable would
break CI (no API keys), force-disable would leave operators
expecting the cycle confused when it didn't fire.

The v1 revision of this ADR proposed auto-enable at session start.
The revised-adversarial pass (R14) flagged that this would call an
LLM-backed hook in CI and break the acceptance suite.

**Decision:**

Auto-enable is **conditional on an active dialect actually being
available**. At session start the engine asks the dialect router for
the active dialect; if no dialect resolves (no API key configured,
no model selected), the adversarial cycle defaults to **disabled**
with a one-line operator banner:

```
ℹ adversarial cycle: disabled (no dialect configured; type `/adversary enable` to wire)
```

The CI path (no API key) sees the banner and never instantiates an
LLM-backed hook. The operator-facing toggle is the `/adversary`
slash command (`enable` / `disable` / bare status).

When enabled, the dispatcher partitions an arrow's clauses on
`DepthType == DepthTypeSensitive` and runs the cycle on the
sensitive partition; verification runs on `robust + auto-inserts`
afterward.

**Consequences:**

- CI without API keys continues to pass without changes.
- Operators with an active dialect get the cycle for free on
  depth-sensitive arrows.
- `/adversary enable` refuses with `no-dialect-configured` when no
  dialect resolves — the refusal is the operator-attestation
  surface, not a silent no-op.
- An operator may explicitly `/adversary disable`; depth-sensitive
  dispatches then refuse with `adversarial-hooks-not-wired` rather
  than running verification-only.
