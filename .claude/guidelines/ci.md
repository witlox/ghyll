# CI/CD Guidelines

## Pipeline Structure (three-stage)

Every project follows: **Build → Validate → Test**

### Build Stage

- Compile all targets
- Create versioned artifacts (binaries, Docker images)
- Upload artifacts with retention (7-30 days)
- Path-based triggers: only run when relevant code changes

### Validate Stage

- Format check (`gofmt -l`)
- Static analysis (`go vet`)
- Linting (`golangci-lint` with zero warnings)
- Module hygiene (`go mod tidy` consistency)
- Security scanning (`govulncheck`, dependabot)

### Test Stage

- Unit tests with race detection (`go test -race`)
- Acceptance tests (Gherkin/godog)
- Coverage per test type → merge → Codecov
- Threshold enforcement (50% minimum, 80% target)
- Separate coverage flags for unit/acceptance

## Triggers

- Push to `main`
- PRs against `main`
- Path exclusions: `docs/**`, `*.md`, `LICENSE`

## Caching

- Go: `actions/setup-go` with `cache: true`
- Module cache: `~/go/pkg/mod`

## Additional Workflows

- **Dependabot auto-merge** for patch/minor updates
- **Vulnerability fix** PRs from `govulncheck`
- **CodeQL** weekly security scan

## Docker Builds

- Multi-stage: builder (full Go SDK) → runtime (distroless or alpine)
- Non-root user in runtime image
- Strip with `-ldflags="-s -w"`
- Version injected at build time
- No privileged mode in CI

## Release

- Version from git tag or calver (e.g., 2026.1.0)
- Binaries attached to GitHub releases
