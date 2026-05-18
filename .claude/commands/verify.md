Pre-commit verification (Tier 1). Run before every commit claim.

1. Format: `gofmt -l .` — must be empty
2. Vet: `go vet ./...` — must be 0 errors
3. Lint: `golangci-lint run --timeout=5m` — must be 0 errors
4. Build: `make build` — must succeed
5. Unit tests: `make test-unit` — all must pass
6. Scenario coverage: `make verify-scenarios` — report uncovered scenarios
7. Report: show pass/fail counts for each step

If ANY step fails, do NOT commit. Fix first, then re-run.

For pre-PR (Tier 2) add:
- `make test` (includes acceptance suite)
- `make test-race`
- `make coverage-check`
