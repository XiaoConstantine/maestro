# TUI action, dispatch, and effect pipeline

## What changed

The canonical Bubble Tea model now keeps its framework boundary small:

```text
tea.Msg
  -> actionFromMessage
  -> MaestroModel.Dispatch (synchronous state reduction)
  -> []Effect
  -> executeEffects
  -> tea.Cmd / task result
  -> Dispatch
```

`Action` is a closed set that distinguishes user keys, resize, scrolling, each typed asynchronous task result, progress ticks, and explicitly ignored unknown Bubble Tea messages. `Dispatch` applies state changes synchronously and returns inert `Effect` descriptions. `Update` only translates, dispatches, and schedules effects. The former large reducer moved from `maestro_model.go` to `dispatch.go`.

Backend calls and cancellation now execute only after dispatch through a `CommandEffect`. This includes both Esc cancellation and coding-result cleanup, which previously canceled contexts directly during reduction. Task-result messages re-enter the same reducer, preserving Bubble Tea's unidirectional loop. Message and progress timestamps use injected clocks so reducer tests can replay with fixed time.

## Why

Mixing backend calls into `Update` made input transitions difficult to test and allowed one key handler to mutate state and perform I/O in the same call stack. The split follows the action/dispatch/effect architecture studied in Grok Build: dispatch is deterministic, while the event loop owns execution.

`CommandEffect` is the Bubble Tea adapter boundary. Future work can introduce more specific effect values without changing `Update` or the dispatch contract.

## Verification

Focused tests prove that model-list and cancellation work do not touch the backend or cancellation context during `Dispatch`, then run exactly once when their effects execute.

```sh
GOWORK=off go test ./terminal
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
