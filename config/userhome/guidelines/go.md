<!-- ghyll bias — edit/delete as needed. -->
# Go bias

- `gofmt` always. No `//nolint:` without an inline rationale.
- `context.Context` threaded through every I/O; never `context.Background()` deep in a call stack.
- Errors are typed sentinels with `Err<Subject><Reason>` names.
- No globals. No package-level mutable state.
- Tests named `TestScenario_<context>_<behavior>`.
- New tests are race-clean; CI runs `go test -race` and rejects red.
- Coverage floor 50% per project; aim ≥60% per new file.
