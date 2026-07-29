# Coding executor boundary

## What changed

`internal/coding.Session` now depends on the small `coding.Executor` contract instead of storing a concrete DSPy-Go `native.Agent`:

```go
type Executor interface {
    ExecuteWithTrace(
        context.Context,
        map[string]any,
        agents.EventSink,
    ) (agents.AgentExecutionResult, error)
    Close(context.Context) error
}
```

The existing native agent is wrapped by an internal adapter, so the default coding path, rooted tool registration, session persistence, trace results, and lifecycle events retain their behavior. `NewSessionWithExecutor` resolves the authoritative workspace first, passes that canonical root to an `ExecutorFactory`, and exclusively owns the returned executor. This allows another implementation to run behind the same session lifecycle without teaching `Session` about that implementation or permitting the session and executor to disagree about their workspace.

## Ownership boundary

The split keeps responsibilities explicit:

- `Session` owns canonical workspace identity, single-run admission, cancellation, executor ownership, and shutdown.
- `Executor` owns model orchestration, tools, persistence, traces, event production, and cleanup of its implementation-specific resources.
- The native adapter owns DSPy-Go's construction-time event-forwarding bridge.

An alternate executor receives the operation-scoped event sink directly. This avoids requiring a future RLM controller to imitate `native.Agent`'s construction-time event sink. When session shutdown begins, active execution is cancelled and executor cleanup is scheduled after that execution terminates. The caller's `Close` context bounds waiting, but a timeout does not abandon the owned executor cleanup.

## Why

Maestro's current coding path is a native tool-calling agent. The planned coding RLM needs a different execution loop with recursive children, shared tree budgets, a restricted environment, and a host-owned tool broker. Those policies belong in Maestro rather than DSPy-Go's generic RLM module, but they should not require duplicating session admission and cancellation logic.

This change introduces only the seam. It does not enable an RLM mode or alter the default native execution path.

## Verification

```sh
GOWORK=off go test ./internal/coding
GOWORK=off go test -race ./internal/coding
GOWORK=off go vet ./internal/coding
GOWORK=off go mod tidy -diff
git diff --check
```
