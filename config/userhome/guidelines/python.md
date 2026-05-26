<!-- ghyll bias — edit/delete as needed. -->
# Python bias

- `ruff format` + `ruff check --select=ALL --extend-ignore=...` (project tunes the ignore list).
- Type hints everywhere; `mypy --strict` or `pyright` in CI tier 2.
- Tests `test_scenario_<context>__<behavior>` under `tests/`; `pytest -x` for the fast tier.
- No mutable defaults (`def f(x=[]):`) — always `None` + sentinel-check.
- Context managers (`with`) for every file/socket/connection; no leaked fds.
- Errors: subclass a domain `Error` base; never bare `Exception`.
- Coverage floor 50% per project; aim ≥60% per new file (`coverage` or `pytest-cov`).
