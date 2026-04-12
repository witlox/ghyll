# ADR-002: Shared Types Leaf Package

Date: April 2026
Status: Accepted

## Context

The original architecture placed `Message` and `ToolCall` types in `context/`, and `ToolResult` in `tool/`. This created hidden import dependencies: `dialect/` functions take `[]context.Message` and return `[]context.ToolCall`, forcing `dialect/` to import `context/`. Similarly, `stream/` returns `ToolCall` in responses, requiring `stream/` to import `context/`.

While Go doesn't allow import cycles (so compilation would catch true cycles), the stated dependency graph was wrong --- `dialect/` and `stream/` had undeclared dependencies on `context/`.

## Decision

Extract shared types (`Message`, `ToolCall`, `ToolFunction`, `ToolResult`) into a `types/` leaf package with zero dependencies. All packages import `types/` freely without creating coupling.

## Consequences

- Package graph is honest --- declared dependencies match actual imports
- Adding a field to `Message` is a single-package change
- `types/` must remain a leaf --- no dependencies allowed
- Slight indirection: `types.Message` instead of `context.Message`
