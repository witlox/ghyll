# Context Management

The context manager (`context/manager.go`) is the single owner of the conversation context window. No other package directly mutates the message list.

## Ownership Model

The manager holds a `[]types.Message` slice protected by a mutex. Other packages interact through:

- `AddMessage()` --- append a message
- `Messages()` --- get a copy of the current window
- `PreTurnCheck()` --- run proactive compaction if needed
- `ReactiveCompact()` --- compact in response to model rejection
- `ApplyBackfill()` --- prepend checkpoint summaries

## Compaction

When the context window exceeds 90% of the active model's limit, compaction triggers:

1. Split messages into "to summarize" and "preserved" (last 3 turns)
2. Send the turns-to-summarize to the model as a separate API call with the dialect's compaction prompt
3. Replace old turns with the model's summary
4. Create a checkpoint capturing the pre-compaction state

This is a **separate API call** --- not the full context window. This prevents the compaction request itself from exceeding the model's limit.

### Reactive Compaction

If the proactive check underestimates and the model rejects with `context_length_exceeded`, reactive compaction fires. The request is retried exactly once after compacting.

## Callback Wiring

The context manager can't import dialect/ or stream/ (would create import cycles). Instead, `cmd/ghyll` provides callbacks at init:

```go
ManagerDeps{
    TokenCount:       dialect.M25TokenCount,
    CompactionCall:   session.compactionCall,  // wires stream.Send
    CreateCheckpoint: session.createCheckpoint, // wires memory.Store
}
```

This keeps the package graph acyclic while allowing cross-cutting flows.
