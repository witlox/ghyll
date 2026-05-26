<!-- ghyll bias — edit/delete as needed. -->
# CI bias

Three test tiers, cascading. Each higher tier includes the lower:

| Tier | Make target | When |
|---|---|---|
| 1 (fast) | `make test-unit` | between every edit, pre-commit |
| 2 (slow) | `make && make test-race && make coverage-check` | pre-PR |
| 3 (full) | tier 2 + `make test-live` (build tag `live`) | pre-merge / nightly |

`make` (no target) = lint + test + build. Run before every commit. Lefthook enforces fmt + lint + test + vet on commit.

Releases tag automatically on the Sunday cron or `gh workflow run release.yml`. Don't `git tag` by hand.
