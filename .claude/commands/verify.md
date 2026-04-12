Pre-commit verification. Run this before every commit claim.

1. Format: `go fmt ./...`
2. Vet: `go vet ./...` — must be 0 errors
3. Lint: `golangci-lint run ./...` — must be 0 errors (if golangci-lint is installed)
4. Build: `make` — must succeed
5. Unit tests: `make test` — all must pass
6. Report: show pass/fail counts for each step

If ANY step fails, do NOT commit. Fix first, then re-run /project:verify.
