# Post-prod-readiness adversarial — C-1/C-2/C-3/H-1/H-2 review

Cold-context review of the five integrator-finding remediations:

- `b6c8c8f` — C-2 config embed + auto-write
- `68f9eb1` — H-2 roles embed
- `f0c79cc` — H-1 concepts embed
- `1c39506` — C-3 dispatcher slash commands
- `b3bd710` — C-1 ghyll init

Surfaces reviewed: `cmd/ghyll/config_bootstrap.go`,
`cmd/ghyll/init_bootstrap_cmd.go`, `cmd/ghyll/init_cmd.go`,
`cmd/ghyll/run_arrow_cmd.go`, `cmd/ghyll/session.go`
(validateOpID + dispatch), `bootstrap/registry.go`,
`bootstrap/session.go`, `bootstrap/grid.go`, `bootstrap/init.go`,
`bootstrap/propose.go`, `bootstrap/profile.go`, `bootstrap/roleclause.go`,
`catalogue/embedded.go`, `runner/operatorbus.go`,
`runner/dispatcher.go`, `assets.go`.

## Critical

| ID | Title | File:line | Why critical |
|---|---|---|---|

_(none)_

## High

| ID | Title | File:line | Why high |
|---|---|---|---|
| H-A | `ghyll init` op-id is not NFC-normalized; grid stores raw form while bootstrap.Session stores normalized form — operator identity drifts across surfaces | `cmd/ghyll/init_bootstrap_cmd.go:81` + `bootstrap/session.go:94` | An operator who types `José` (composed) via `ghyll init` lands `"café"`-style bytes in `grid.v1.yaml`. The same operator typing the decomposed form via `/op-id` is normalized to NFC before recording — equality across the two surfaces breaks. Audit-trail reconciliation between the grid's `created-by-op-id` and the session's op-id silently disagrees. The two validators (cmd/ghyll local `validateOpID` vs `bootstrap.ValidateAndNormalizeOpID`) need to share one canonical implementation. |
| H-B | `ghyll run` first-run config bootstrap surfaces a misleading "file exists" error when two `ghyll run` invocations race | `cmd/ghyll/config_bootstrap.go:47` | Process A: `Load` → not-found → `OpenFile(O_CREATE\|O_EXCL)` succeeds, `errConfigBootstrapped` returned, exits 0. Process B (parallel): `Load` → not-found (A hasn't finished yet) → `OpenFile(O_CREATE\|O_EXCL)` → EEXIST. The wrapped error reads `write default config <path>: open <path>: file exists` — the second operator thinks something is wrong, when in reality the config was just written by the other process. Should detect EEXIST, retry `Load`, and either return the parsed cfg or propagate the real error. |
| H-C | `/run-arrow` does not nil-guard `bus := s.engine.Bus()` before calling `bus.Subscribe(...)` — if Bus returns nil (defensive future refactor / partial init), the slash command nil-derefs in the REPL | `cmd/ghyll/run_arrow_cmd.go:165` | `engineRuntime.Bus()` is documented to return nil when the receiver is nil. The handler already guards `s.engine == nil`, but the contract between `engine != nil` and `Bus() != nil` is only enforced by construction (line session_engine.go:160). A future refactor that splits engine open from bus wiring (e.g., to support lazy initialization or a vault-backed bus) would silently nil-deref here. Defense-in-depth: check `bus == nil` and surface a clean error message. |

## Medium

| ID | Title | File:line | Why medium |
|---|---|---|---|
| M-A | `scanBrownfieldContexts` does not enforce `MaxBoundedContexts` | `bootstrap/profile.go:299` | DeclareContext caps at 256 contexts; brownfield auto-scan can return arbitrarily many (one per `src/<name>/` dir). A pathological repo with 5000 `src/*/` dirs produces 5000 contexts × 4 role pairs = 20 000 arrows in the resulting grid. Not a crash but a slow init + large YAML. Should mirror DeclareContext's cap. |
| M-B | `ghyll init` writes `.ghyll/` with mode 0o755 (default of `os.MkdirAll`) — operator-private config (0o600) elsewhere | `bootstrap/grid.go:177` | The grid is project-shared and 0o644 is appropriate. The CONTAINING `.ghyll/` directory at 0o755 is also fine for the grid, BUT `.ghyll/engine.db` lives in the same dir and contains attestation records that include op-id. The directory perm grants `o+x` so other users on the system can `stat .ghyll/engine.db`. Engine perm hardening is out of scope for this pass; flag for follow-up. |
| M-C | `ghyll init` `--op-id` validation does not share validator implementation with `/op-id` or `bootstrap.ValidateAndNormalizeOpID` | `cmd/ghyll/session.go:1444` | Three separate validators (cmd/ghyll `validateOpID`, `bootstrap.ValidateAndNormalizeOpID`, the per-session driver) all enforce overlapping-but-not-identical rules. Specifically: the cmd/ghyll validator rejects leading `.` and `-` and trailing `.` that the bootstrap one does not; the bootstrap one NFC-normalizes which the cmd/ghyll one does not. Consolidate. Linked to H-A. |

## Low

| ID | Title | File:line | Why low |
|---|---|---|---|
| L-A | `/run-arrow` event subscriber may miss a trailing event between `defer unsubscribe()` setup and the final `mu.Lock()` snapshot | `cmd/ghyll/run_arrow_cmd.go:170-195` | If a publisher fires AFTER the slash command's final `captured := append(...)` but BEFORE `defer unsubscribe` runs, the event is appended to `events` and then dropped (the local `events` slice is unreferenced). Not a correctness issue (the modal driver subscribes independently and surfaces escalations through its own queue), but the inline render of `/run-arrow` events is racy with publishers. Acceptable for now; document. |
| L-B | `ghyll init` default-context auto-declaration is silent in the success summary | `cmd/ghyll/init_bootstrap_cmd.go:125` | The status line says "init complete: N arrows across M contexts" but doesn't call out that the M=1 case was synthesized. The operator who runs init in a multi-context repo can miss the "no contexts detected" signal. Add an inline hint. |
| L-C | `ghyll init` BuildProposal residue carries the raw error string of `ErrClauseArgsIncomplete`, which interpolates `mapKeys(args)` — Go map iteration order is non-deterministic so the residue reason text is not reproducible | `bootstrap/propose.go:413` | The reason field is for human/audit consumption; a non-deterministic ordering means re-running `ghyll init` on the same repo and catalogue can produce different-looking residue strings. Cosmetic; sort the keys before formatting. |

## Remediation pass

Remediations target Critical + High per the standing user direction
("no deferrals on adversarial-pass findings"). Medium and Low are
flagged above and remain for the next triage.

### H-A: unified op-id validator

Route `ghyll init` (and the two `cmd/ghyll/init_*` paths) through
`bootstrap.ValidateAndNormalizeOpID` so the grid's `created-by-op-id`
matches what the bootstrap.Session would store. The cmd/ghyll local
`validateOpID` is kept as a thin shim for `/op-id` and `/attest` so
the REPL surface preserves its specific dot/dash rules while still
NFC-normalizing the result.

### H-B: clean error on first-run config race

Detect `errors.Is(openErr, os.ErrExist)` after `O_EXCL` failure and
re-`Load` the file. If `Load` then succeeds, return the parsed cfg
(the racing process won). If `Load` fails with a parse error,
surface that real error. If `Load` reports not-found again (file
yanked between our checks), bail with a clear "concurrent
modification" message.

### H-C: nil-guard `bus` before Subscribe in `/run-arrow`

A one-line `if bus == nil` defensive check that surfaces a clean
error message rather than nil-derefing the bus pointer.

## Verification

After remediation: `make` (lint + test + build).
