<!-- ghyll bias — edit/delete as needed. -->
# /verify — pre-commit.

Before any commit:

1. `make` — lint + unit + acceptance + build.
2. `make test-race` — race-detector clean.
3. `make coverage-check` — 50% floor.
4. `grep -rn "TODO\|FIXME" $(git diff --name-only HEAD)` — zero new TODOs; file a GH issue instead.
5. `/list-arrows` — every arrow your work touched still has `unevaluated`=0 or a deliberate residue note.

Refuse the commit if any of the five fails.
