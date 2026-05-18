# Go Guidelines

## Version & Tooling

- Go 1.25+
- Format: `gofmt` (enforced) + `goimports` (preferred)
- Lint: `golangci-lint` with zero warnings
- Vet: `go vet ./...` clean
- Coverage: `go tool cover` → Codecov; 50% minimum, 80% target

## Style

- Standard library preferred over third-party
- `context.Context` threaded through all I/O operations
- No globals, no `init()` side effects
- Receivers consistent within a type (all pointer or all value)
- Exported identifiers documented with godoc

## Error Handling

- Wrap with `fmt.Errorf("op: %w", err)` for context
- Sentinel errors with `errors.Is`; typed errors with `errors.As`
- No `panic` outside `main` and intentional invariant violations
- `recover` only at goroutine boundaries with explicit rationale

## Testing

- Table-driven where the same logic applies to multiple inputs
- `testify` for assertions (project preference)
- `-race` detector in CI
- `testdata/` directory for fixtures
- Acceptance tests via `godog` (BDD)
- Subtests with `t.Run` for grouped cases

## Build System

- `Makefile` orchestrates fmt, lint, test, build
- `make` (no target) runs full pre-commit pipeline
- Versioned binaries via `-ldflags="-X main.version=..."`

## CI Pipeline

Three-stage (see `guidelines/ci.md`):
1. Build — `go build`, upload binary artifacts
2. Validate — `gofmt -l`, `go vet`, `golangci-lint`
3. Test — `go test -race`, coverage to Codecov, threshold enforcement

## Patterns

- Concrete types over interfaces unless multiple implementations exist
- Functional options for variadic configuration
- `errgroup` for bounded concurrent work
- `context.WithTimeout` on every network I/O
