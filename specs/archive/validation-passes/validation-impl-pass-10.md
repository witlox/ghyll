# Validation pass 10 — adversarial review of phase 10 work

Cold-context adversarial pass on phase-10 session-loop wiring
(engine integration, commit-per-model-change handoff, `ghyll engine`
CLI). Three parallel adversaries, 45 findings total.

**Severity distribution:** 0 Critical, 14 High, 22 Medium, 9 Low.

**Per user direction:** fix all findings, no deferrals.

Adversary numbering: `Wn` = session-engine wiring, `Hn` =
handoff flush + router actions, `Cn` = ghyll engine CLI.

---

## High

### H3 — §7.1 outcomes warn the operator AND then dispatch on the insufficient tier
`cmd/ghyll/session.go:411-431`. ActionGateUnsatisfiable /
ActionGateLockedConflict / ActionInvalid emit a warning, then fall
through to `s.sendAndProcess()` — sending the message to a model
that may not satisfy the gate's depth. §7.1 says "never laundered."
The warning is decorative.

**Fix:** in the three §7.1 branches, return a structured
attestation-pending response BEFORE sendAndProcess. The chat loop
must NOT dispatch.

### W1 — `attachJournal` is non-idempotent; second call duplicates writes + leaks goroutines
`cmd/ghyll/session_engine.go:111`. Each call appends new observers
but never detaches old ones; old `*Journal` instance survives via
closure references, its consumer goroutine and channel leak.

**Fix:** guard `if r.journal != nil` and return an error; document
"call exactly once" in `engineRuntime.attachJournal`. Add a test.

### W2 — `NewRunner` permanently disables §6/§7.1 depth enforcement
`cmd/ghyll/session_engine.go:145-152`. Returns a runner with
`actualTier == DepthRankNone`, which disables the short-circuit
that would route depth-sensitive clauses to Unevaluated.

**Fix:** require a `tier DepthRank` parameter on `NewRunner`; chain
`.WithActualTier(tier)`. Document that the dispatcher must supply it.

### W3 — `replayEngine` runs on `context.Background()` with no timeout
`cmd/ghyll/session.go:227`. Large or stalled replay blocks NewSession
indefinitely with no operator signal.

**Fix:** `gocontext.WithTimeout(..., replayTimeout)` (default 30s,
configurable via `SessionConfig`). Emit a "replaying engine state…"
status line on entry.

### W4 — Engine init failure path is untested
`cmd/ghyll/session.go:223-245`. The "graceful degradation" claim has
zero coverage. Open failure, replay failure, DisableEngine flag —
all untested at the `NewSession` integration boundary.

**Fix:** add `TestPhase10_NewSession_EngineOpenFailureFallsBack`,
`TestPhase10_NewSession_EngineReplayFailureClosesStore`,
`TestPhase10_NewSession_DisableEngine`.

### W5 — Re-opening the same engine.db twice in one process runs schema DDL on a live database
`cmd/ghyll/session_engine.go:60`. `OpenStore` always runs
`CREATE TABLE IF NOT EXISTS` + composite-index create on every
open. A second open (e.g., `ghyll engine status` against a live
session) races DDL against running mutations.

**Fix:** new `OpenStoreReadOnly` flavor that skips schema migration.
`engine_cmd` uses it.

### H1 — `PendingUnknown` silently treated as "no flush needed"
`cmd/ghyll/session.go:274-281`. The current err-branch dominates but
the `!= PendingStaged` check would silently skip a future
`(PendingUnknown, nil)` return path.

**Fix:** explicit switch on every PendingStatus value; surface
warning for Unknown.

### H2 — Shared 5-second ctx used for both CheckPending and GitCommit
`cmd/ghyll/session.go:272-289`. After CheckPending consumes ~4s of
the budget, GitCommit gets ~1s — well below the documented 30s.
The constants are out of sync.

**Fix:** independent `WithTimeout` per call OR a single combined
context wider than max(checkTimeout, commitTimeout).

### H4 — Implicit default branch in routing switch — future actions silently no-op
`cmd/ghyll/session.go:411-428`. No `default` clause; any future
Action constant falls through to `sendAndProcess` silently.

**Fix:** explicit `default:` branch logging "unhandled action".

### H5 — `PendingUnstaged` silently misattributed to the NEXT model
`cmd/ghyll/session.go:278-281`. Spec gap: "pending changes" — does
that include unstaged? Current code skips unstaged silently.

**Fix:** pick one policy and document. Conservative: emit a warning
and refuse the switch (operator must clean tree) for Unstaged.

### C1 — `ghyll engine status` silently truncates counts at 1000
`cmd/ghyll/engine_cmd.go:80-109`. Each ListX call caps at 1000. A
project with 5000 findings sees "findings: 1000."

**Fix:** add `engine.CountX` methods using `SELECT COUNT(*)`. CLI
uses those, not ListX.

### C2 — Unbounded per-row-error dump on replay failure
`cmd/ghyll/engine_cmd.go:157-162`. Floods stdout with 100k+ lines
on a corrupt DB.

**Fix:** cap printed errors at 50; emit "… N more errors elided".

### C3 — Per-row error strings printed verbatim (terminal-control + free-text leak)
`cmd/ghyll/engine_cmd.go:160`. ANSI escapes, `\r`, etc. flow through
to the terminal.

**Fix:** sanitize each `e` via a control-byte-stripping helper at
the print boundary.

### C4 — Sqlite error text leaked verbatim in CLI error returns
`cmd/ghyll/engine_cmd.go:74-75, 136-137`. Internal schema names,
file paths in operator output. Same class as validation-pass-9 V3.

**Fix:** classify errors at CLI boundary; print operator-friendly
message; keep verbose available via `--verbose`.

---

## Medium

### W6 — No invariant guard enforces replay-before-attachJournal ordering
`cmd/ghyll/session_engine.go:104-117`. Inverted order causes
recursive journaling.

**Fix:** `journalAttached` flag on engineRuntime; replayEngine
errors if true; attachJournal errors if already true.

### W7 — `defaultEngineDBPath` accepts `..` traversal and follows symlinks
`cmd/ghyll/session_engine.go:80-89`. Engine.db can escape the
project directory.

**Fix:** `filepath.EvalSymlinks` + containment check; reject if
result not under absolute workdir.

### W8 — Per-row replay errors surfaced as a single count line; no triage surface
`cmd/ghyll/session.go:240-242`. Errors slice is discarded after
print.

**Fix:** log up to 10 error strings verbatim (sanitized);
optionally write full list to `.ghyll/engine.replay.errors.log`.

### W9 — `engineRuntime.NewRunner` is not safe to call concurrently
`cmd/ghyll/session_engine.go:145-152`. Observer-slice append races
under `-race`.

**Fix:** document single-goroutine handle; or add mutex to
Runner.runObservers (`runner.OnEvaluationRun`).

### W10 — `closeEngine` runs `journal.Close()` synchronously with no overall cap
`cmd/ghyll/session_engine.go:130-140`. Worst-case 85 minutes if the
channel is full and disk is slow.

**Fix:** overall close deadline (30s); force-cancel beyond.

### W11 — Dropped journal events at session-end are silently invisible
`cmd/ghyll/session_engine.go:130-140`. `journal.Dropped()` is never
read at shutdown.

**Fix:** before closing, read `journal.Dropped()`; if non-zero,
emit `s.output("ℹ journal dropped %d events at shutdown")`.

### W12 — `s.engine = nil` not set after Close; double-Close hits driver-error path
`cmd/ghyll/session.go:252-257`. Latent today (one defer); a future
second-Close would error.

**Fix:** `sync.Once`-gated Close; or set `s.engine = nil` after
closeEngine.

### H6 — Endpoint URL leaks into git log via Ghyll-Model trailer
`cmd/ghyll/session.go:302-307`. Internal infrastructure DNS exposed
in every model-switch commit on a public mirror.

**Fix:** stamp model NAME only, or hash the endpoint. New optional
`cfg.Models[name].StampLabel` overrides.

### H7 — handleHandoff creates checkpoint even after flush fails
`cmd/ghyll/session.go:603-617`. Audit trail says "handed off" but
staged changes are still in the working tree.

**Fix:** on flush error, abort the handoff (don't switch model) OR
record the flush failure on the checkpoint via a distinct Reason.

### H8 — handoff checkpoint summary embeds model names without sanitization
`cmd/ghyll/session.go:614`. Model names with embedded newlines
corrupt the checkpoint summary.

**Fix:** sanitize via `sanitizeOneLine` analogue before formatting.

### H9 — `decision.Reason` missing from handoff checkpoint summary
`cmd/ghyll/session.go:614`. Operators can't tell why handoff
happened from `ghyll memory log`.

**Fix:** include `decision.Reason` in the summary string.

### H10 — `s.workdir == ""` returns nil silently; operator never warned
`cmd/ghyll/session.go:269-271`. Stamping silently disabled.

**Fix:** one-shot warning at session start when workdir is empty.

### H11 — ActionInvalid message hides actual GateFloor value
`cmd/ghyll/session.go:426-427`. Operator can't triage without
reading source.

**Fix:** extend RoutingDecision with a `RejectedFloor int` field;
surface in the warning.

### H12 — Flush-test coverage limited to the clean-tree path
`cmd/ghyll/session_engine_test.go:180-207`. Only happy-path
covered.

**Fix:** add tests for staged-triggers-commit, Unstaged-only,
CheckPending error, commit failure, malformed model name, §7.1
dispatch-vs-attestation.

### H13 — `newMinimalConfigForFlush` returns zero-valued RoutingConfig
`cmd/ghyll/session_engine_test.go:17-25`. Implicit coupling
between helper and implementation contract.

**Fix:** populate Routing; document which fields the flush helper
depends on.

### C5 — No timeout on `engine.Replay` in `cmdEngineReplay`
`cmd/ghyll/engine_cmd.go:146`. CLI hangs forever on a slow DB.

**Fix:** `context.WithTimeout(..., 60s)` default; `--timeout`
override.

### C6 — `engine.db` is a directory — misleading error path
`cmd/ghyll/engine_cmd.go:69-76, 130-137`. `os.Stat` doesn't check
`IsDir()`.

**Fix:** after Stat, if `info.IsDir()`, return typed error.

### C7 — Future-schema DB silently looks empty
`cmd/ghyll/engine_cmd.go:73 + engine/store.go:57`. Renamed columns
fail with internal column names exposed.

**Fix:** add `schema_version` to engine.Store; reject mismatches at
open with operator-friendly message.

### C8 — Misleading error for trailing positional args ("unknown flag")
`cmd/ghyll/engine_cmd.go:40-53`.

**Fix:** if `args[i]` doesn't begin with `--`, emit
"unexpected positional argument".

### C9 — No length cap on `--dir` argument (V11 reprise at CLI)
`cmd/ghyll/engine_cmd.go:48, 54-58`. Multi-MB `--dir` causes
unbounded allocation.

**Fix:** cap `len(dir) <= 4096` at parseEngineFlags entry.

### C10 — Replay-internal error swallows partial progress
`cmd/ghyll/engine_cmd.go:146-149`. On `engine.Replay` error, the
counts of phases that succeeded are invisible.

**Fix:** print `counts` first, then return the error.

### C11 — `status` emits no exit-distinguishing signal for missing DB
`cmd/ghyll/engine_cmd.go:69-72, 130-133`. Scripts can't detect
"no engine yet" vs "engine empty".

**Fix:** structured first-line token in human output;
`--format json` reserved.

---

## Low

### W13 — `buildModelStamp` accepts unsanitized model names; the v8 rejection path is untested
`cmd/ghyll/session.go:302-307`. Defense-in-depth holds (commit.go
rejects), but regression untested.

**Fix:** add `TestPhase10_BuildModelStamp_RejectedByCommit`.

### W14 — Engine init logger is uniformly `log.Default()`; multi-session log lines interleave
`cmd/ghyll/session.go:224,232`. Latent (one session per process today).

**Fix:** thread a session-scoped logger through SessionConfig.

### W15 — `TestPhase10_NewRunnerAttachesJournal` doesn't assert isolation
`cmd/ghyll/session_engine_test.go:142-175`. Wouldn't catch
double-attach.

**Fix:** add `TestPhase10_AttachRunnerTwice_DoesNotDuplicate`.

### H14 — `decision.Reason` interpolated into commit body without sanitization
`cmd/ghyll/session.go:283-284`. Defense-in-depth low likelihood.

**Fix:** validate `decision.Reason` via `hasControlOrLineSep` before
formatting.

### H15 — `gitCheckTimeout`/`gitCommitTimeout` are hard-coded
`cmd/ghyll/session.go:309-312`. No config override (deviates from
project's pattern).

**Fix:** add `cfg.Tools.GitCheckTimeoutSeconds` /
`GitCommitTimeoutSeconds`; default 5/30.

### C12 — Two ListAmendments round-trips where one would do
`cmd/ghyll/engine_cmd.go:96-105`. Subsumed by C1 (count via SQL).

### C13 — Zero test coverage on phase 10 slice 3
`cmd/ghyll/engine_cmd.go` (entire file).

**Fix:** `engine_cmd_test.go` with table-driven tests for
parseEngineFlags + integration tests for status/replay.

### C14 — Usage string drift between main.go and engine_cmd.go

**Fix:** single `const engineUsage` referenced from both sites.

### C15 — Status output is an unspecified wire contract

**Fix:** doc comment declaring output unstable; reserve
`--format json` for future machine consumers.

---

## Highest-risk areas

1. **§7.1 dispatch bypass (H3)** — the chat loop laundering happens
   despite the operator warning. Single most direct spec violation.
2. **Engine wiring lifecycle (W1, W3, W4, W5)** — non-idempotent
   attach, no replay timeout, untested failure paths, schema DDL
   races across opens. Together: persistence layer fragility.
3. **CLI accuracy + safety (C1, C3, C4)** — silent count truncation
   undermines the tool's value; control-char + sqlite-text leaks
   harm operator triage.

## Remediation plan

No deferrals. Order chosen so structural fixes don't churn later changes:

1. §7.1 dispatch fix (H3) — the chat loop's return path.
2. Engine wiring lifecycle: attachJournal idempotency (W1), Runner
   tier parameter (W2), replay timeout (W3), DisableEngine + open
   failure + replay failure tests (W4), ReadOnly open (W5),
   ordering invariant (W6).
3. Handoff hardening: PendingStatus exhaustive switch (H1, H4),
   independent timeouts (H2, H15), Unstaged policy (H5), abort on
   flush error (H7), sanitization + reason (H8, H9, H14),
   workdir warning (H10), info-leak label (H6), RejectedFloor (H11).
4. CLI hardening: Count* methods replacing ListX truncation (C1),
   error sanitization + cap (C2, C3), error classification (C4),
   replay timeout (C5), IsDir check (C6), schema version (C7),
   parse error messages (C8), --dir cap (C9), partial-counts on
   replay error (C10), structured status output (C11, C15).
5. Test additions covering each structural fix (W4, W13, W15, H12,
   H13, C13).

---

## Remediation status

All 45 findings remediated (2026-05-19). No deferrals.

- §7.1 dispatch (H3) → `attestationPendingResponse` blocks the chat
  loop on Unsatisfiable / LockedConflict / Invalid; never sends to a
  model below the gate floor (`cmd/ghyll/session.go`).
- Engine wiring (W1–W6) → `attachJournal` idempotent + ordering-
  invariant guarded via `replayDone` / `journalAttached` flags;
  `NewRunner` requires a `DepthRank` tier; replay runs under a
  `defaultReplayTimeout`; `OpenStoreReadOnly` lands on the CLI path
  so DDL never races a live session; engine_meta schema_version
  rejects future binaries (`cmd/ghyll/session_engine.go`,
  `engine/store.go`).
- Handoff (H1, H4–H11, H14, H15) → exhaustive `PendingStatus`
  switch, default routing arm, Unstaged refused, flush-error
  recorded as `handoff-flush-failed` checkpoint reason, model
  names and Reason sanitized, RejectedFloor on RoutingDecision,
  empty-workdir one-shot warning, StampLabel/name-only commit
  stamp (no endpoint), per-tool git timeouts in config.
- CLI (C1–C11, C15) → `Count*` methods, sanitized + capped per-row
  error dump, `classifyCLIError` for sqlite text, `--timeout`,
  IsDir check, schema-mismatch surfacing, positional-arg vs
  unknown-flag distinction, `--dir` length cap, partial counts on
  replay error, structured `ghyll-engine-status: missing|empty|present`
  first-line tokens.
- Tests (W4, W13, W15, H12, H13, C13) →
  `TestPhase10_AttachJournalIdempotency`,
  `TestPhase10_ReplayAfterAttachErrors`,
  `TestPhase10_AttachJournalBeforeReplay`,
  `TestPhase10_AttachRunnerTwice_DoesNotDuplicate`,
  `TestPhase10_FlushStagedBeforeModelSwitch_StagedTriggersCommit`,
  `TestPhase10_FlushStagedBeforeModelSwitch_UnstagedRefuses`,
  `TestPhase10_FlushStagedBeforeModelSwitch_EmptyWorkdirWarns`,
  `TestPhase10_InitEngine_OpenFailureFallsBack`,
  `TestPhase10_InitEngine_ReplayTimeoutHonored`, full
  `engine_cmd_test.go` covering parse / preflight / classify /
  count.

`make` clean; `go test -race ./cmd/ghyll/ ./dialect/ ./engine/
./config/` clean.
