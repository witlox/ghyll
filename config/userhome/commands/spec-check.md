<!-- ghyll bias — edit/delete as needed. -->
# /spec-check — after a spec or ADR change.

A spec change can silently invalidate Gherkin scenarios. Re-run:

1. `grep -rn "@deferred" specs/features/` — confirm no scenario you were about to lift moved status.
2. `make test-unit` then `make test` — the acceptance suite covers the spec. Green = the prose change is consistent with running code.
3. `ghyll arrow show <touched-arrow>` for any arrow whose clauses reference the changed concept.
