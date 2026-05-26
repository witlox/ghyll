<!-- ghyll bias — edit/delete as needed. -->
# C++ bias

- `clang-format` + `clang-tidy` (`bugprone-*`, `cert-*`, `cppcoreguidelines-*` minimum). C++20 floor.
- RAII: no raw `new`/`delete`. `std::unique_ptr` default; `std::shared_ptr` only with a comment justifying shared ownership.
- `noexcept` only when the body actually is — no opportunistic noexcept.
- Tests via GoogleTest: `TEST(Suite, Scenario_<context>_<behavior>)`.
- `cmake --preset` builds; `ctest --output-on-failure` runs.
- No globals; no `static` mutable state. Singletons need an ADR.
- CI tier 2 runs ASAN + UBSAN + TSAN. Don't suppress without a documented reason.
- Coverage via `llvm-cov`; 50% floor.
