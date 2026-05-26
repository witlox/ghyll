<!-- ghyll bias — edit/delete as needed. -->
# Rust bias

- `cargo fmt` + `cargo clippy -- -D warnings`. MSRV declared in `Cargo.toml`.
- `unwrap()` / `expect()` only in tests, or in production with a `// SAFETY:` / `// INVARIANT:` line explaining why.
- `#[must_use]` on any function returning a wrapper a caller might drop.
- Errors: library code uses `thiserror`; binaries / tests may use `anyhow`. Never `Box<dyn Error>` in a public library API.
- Tests `#[test] fn scenario_<context>_<behavior>()` (snake_case per Rust convention).
- `unsafe` blocks require a `// SAFETY:` comment AND a unit test that exercises the invariant.
- Coverage via `cargo-llvm-cov`; 50% floor.
