# ADR-004: Tool Call Depth Limit

Date: April 2026
Status: Accepted

## Context

The session loop processes tool calls recursively: model returns tool calls, ghyll executes them, sends results back, model may return more tool calls. A compromised or buggy model endpoint could return tool calls indefinitely, causing unbounded recursion, stack overflow, or unbounded context growth.

This was identified during adversary review as a critical severity finding.

## Decision

Hard limit of 50 sequential tool calls per turn. After 50 tool calls without user input, the session returns an error and waits for the next user prompt.

The limit is a constant (`maxToolDepth = 50`), not configurable. Making it configurable would invite setting it to unlimited, defeating the purpose.

## Consequences

- Protects against runaway model loops
- 50 is generous --- normal coding tasks rarely exceed 10 sequential tool calls
- If a legitimate task needs >50 tool calls, the user can continue in the next turn
- The model sees the error message and can adjust its approach
