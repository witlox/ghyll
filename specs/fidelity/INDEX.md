# Fidelity Index

Last checkpoint: 2026-05-19
Status: CHECKPOINT

## Summary

Single behavioral suite under `specs/features/`. The earlier v1↔v2
split is consolidated; what was the "v2-new" surface (adversarial,
amendment, attestation, init, runner, runner-step3, state-machine)
now lives alongside the long-standing surface features in one tree.

| Layer | Scenarios | Notes |
|---|---|---|
| Total scenarios in `specs/features/` | 299 | Single suite, BDD via godog |
| Passing | 294 | THOROUGH or MODERATE bindings to real production code |
| Pending | 5 | `godog.ErrPending` for known-deferred outline rows |
| Skipped via tag filter `~@deferred` | ~65 | Scenarios that depend on code surface not yet shipped (attestation flow, ProjectStatus aggregator, crash recovery) |

The previous tracking framed v1 SHALLOW scenarios separately from the
v2-new NONE block. After the lift, those state-theater scenarios call
real `Session`, `runner.Runner`, `engine.Store`, and `bootstrap` code;
they no longer warrant a separate category. NONE-class scenarios are
gone outside the `@deferred` filter.

## Strict mode

`tests/acceptance/acceptance_test.go` runs with `Strict=false`. The
filter `~@deferred` skips scenarios that depend on code surfaces not
yet built (attestation event bus, Pass entity, ProjectStatus
aggregator, crash recovery, edit-failure fault-injection harness).

Pending-step axis is clean at v1.0.0: every previously-pending step
row (4 in `state-machine.feature` outlines, 1 in `edit.feature`) is
now under a `@deferred` Examples or scenario tag. Flipping
`Strict=true` would surface a separate axis — 13 distinct duplicate
step-regex registrations across step files (e.g., `^a running session
with model "([^"]*)"$` bound in both `steps_web.go` and
`steps_session_features.go`). Godog reports these as "ambiguous" and
runs whichever bound first; the duplicates need to be consolidated
to single registrations before `Strict=true` is safe. Scoped as
post-v1.0.0 cleanup.

## Package fidelity

| Package | Tests | THOROUGH | MODERATE | SHALLOW | NONE | Confidence |
|---|---|---|---|---|---|---|
| `bootstrap/` | 40+ | 30 | 10 | 0 | 0 | HIGH |
| `catalogue/` | 18 | 18 | 0 | 0 | 0 | HIGH |
| `cmd/ghyll/` (session + REPL + CLI subcommands) | 60+ | 24 | 20 | 16 | 0 | HIGH |
| `config/` | 9 | 9 | 0 | 0 | 0 | HIGH |
| `context/` | 10 | 8 | 2 | 0 | 0 | HIGH |
| `dialect/` | 25 | 0 | 25 | 0 | 0 | HIGH |
| `engine/` (sqlite store + journal + replay) | 23 | 0 | 23 | 0 | 0 | HIGH |
| `memory/` | 22 | 14 | 8 | 0 | 0 | HIGH |
| `runner/` (state machine, observers, routing, runner) | 80+ | 30 | 45 | 5 | 0 | HIGH |
| `stream/` | 15 | 8 | 5 | 0 | 2 | HIGH |
| `tool/` | 30+ | 24 | 6 | 0 | 0 | HIGH |
| `ui/` | 6 | 6 | 0 | 0 | 0 | HIGH |
| `vault/` | 14 | 6 | 8 | 0 | 0 | HIGH |
| `workflow/` | 10 | 10 | 0 | 0 | 0 | HIGH |

Coverage: 73.9% across all packages (floor 70%; stretch target 80%).

## Interface fidelity

| Boundary | Functions | Rating |
|---|---|---|
| runner ↔ engine (Observer + Journal) | FindingsStore.Observe, ClassificationsStore.Observe, Grid.Observe, AmendmentQueue.Observe, Runner.OnEvaluationRun, Journal.Attach* | FAITHFUL |
| engine ↔ vault (v2 endpoints) | Server.AttachEngine + 7 /v2/* handlers | FAITHFUL (pagination metadata + auth + 405 + filter coverage) |
| session ↔ engine (engineRuntime) | openEngine, replayEngine, attachJournal, NewRunner(tier), closeEngine | FAITHFUL (idempotency + ordering tests; lifecycle mutex protects races) |
| session ↔ dialect (RoutingDecision) | dialect.Evaluate → Session.Turn dispatch | FAITHFUL (attestation-pending blocks §7.1; RejectedFloor surfaced) |
| cmd ↔ ui | ui.Info/Status/Errorf/Usage/Print + ui.SetOutput | FAITHFUL (byte-level unit tests on every exported function) |
| library ↔ log/slog | engine.Journal, memory.SyncLoop emit slog records routed to `<dir>/.ghyll/ghyll.log` during REPL | FAITHFUL |

## Decision enforcement

| ADR | Decision | Status |
|---|---|---|
| 001 | Architecture (Go, no interfaces, orphan branch, Merkle DAG) | ENFORCED |
| 002 | Types leaf package | ENFORCED |
| 003 | Embedding excluded from hash | ENFORCED |
| 004 | Tool depth limit | ENFORCED |
| 005 | Compaction separate API call | ENFORCED |
| 006 | One session per repo (lockfile) | ENFORCED |
| 007 | Tier-based routing + dialect families | ENFORCED (router reads DefaultModel/DeepModel; dialect allow-list; legacy-string normalization) |
| 008 | Fixed v2 roles, no runtime overlay | ENFORCED (workflow.Load no longer reads .claude/roles or .ghyll/roles) |
| v2-001..013 | v2 ADR series (state-space frame, four-role diamond, synthetic role-ids, concept catalogue, per-concept schemas, hybrid artifact IDs, arrow-pass identity, three locks, versioned grid, init auto-propose, amendment serialization, plus pending tightenings) | ENFORCED (see `docs/decisions/v2/` for per-ADR scope) |

| Invariant (`specs/architecture/gates.md`) | Test | Status |
|---|---|---|
| §6 depth-types declared per clause | `runner/routing_test.go:67–93` | ENFORCED |
| §7.1 unevaluated never laundered (chat-loop) | `cmd/ghyll/session.go` attestation-pending response | ENFORCED |
| §7.1 depth-below-required short-circuit | `runner/runner.go` (WithActualTier + tests) | ENFORCED |
| §8 routing rule (max-tier across clauses) | `dialect/router_test.go` (25 scenarios) | ENFORCED |
| §11 adversarial phase | `runner/adversarial.go` + tests | ENFORCED |
| §12 on-the-spot arrow creation | `runner/onthespot.go` + tests | ENFORCED |
| Commit-per-model-change attribution | `cmd/ghyll/session_engine_test.go` (flush + stamp tests) | ENFORCED |
| Engine schema_version mismatch | `engine/store.go` ensureSchemaVersion + ErrEngineSchemaMismatch | ENFORCED |
| Journal backpressure (drop only on full budget) | `engine/journal.go` enqueueBackpressureBudget | ENFORCED |
| §7.1 strict newer-wins persistence | `engine/records.go` (`store_version > existing`) | ENFORCED |

## Deferred surface (`@deferred` tag)

Scenarios that depend on code not yet shipped. Skipped via godog tag
filter so the surface remains visible in specs without breaking the
green suite. Owners surface them when the relevant code lands:

- Full attestation operator-event bus + JSONL verdict records
  (attestation.feature)
- Pass entity + checkpoint log (state-machine.feature)
- ProjectStatus aggregator + crash recovery (state-machine.feature,
  runner.feature)
- Multi-round orchestrator-level remediation + producer-fix-signal
  (adversarial.feature)
- Operator-facing UX for arrow inspection (`ghyll arrow show <id>`,
  not yet implemented)

## Remaining narrow gaps

| Scenario | Reason | Risk |
|---|---|---|
| Tier fallback auto-routing (stream) | Orchestration in REPL/session layer, not stream | Low — retries tested, fallback is routing logic |
| Backfill from team memory (drift) | Requires live embedder + vault + store | Medium — individual components tested |
| Concurrent push conflict (sync) | Requires concurrent git processes | Low — append-only design prevents conflicts |
| Large repo clone optimization (sync) | Shallow fetch not testable without large repo | Low |

## Race + CI

`make test-race` clean as of 2026-05-19. The `runner/subprocess.go`
`killProcessGroup` path uses an atomic `reaped` flag plus
`syscall.Kill(pid, 0)` POSIX liveness probe — same hook applied to
`arrowartifact.go` and `killserver.go` callers.
