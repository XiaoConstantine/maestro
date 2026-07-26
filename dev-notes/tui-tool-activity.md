# Persistent coding-run activity

## What changed

The terminal-facing `CodingEvent` projection now preserves canonical run ids, event timestamps, turn/max-turn numbers, tool indexes, and terminal turn/tool counts. Provider-neutral `agents.ExecutionEvent` values remain the source of truth; the TUI does not parse provider output.

`ToolActivityModel` consumes that projection and retains one structured block for the current coding run:

- run start and terminal summary
- turn boundaries
- matched tool start/finish entries
- elapsed tool duration when timestamps are available
- collapsed result evidence with keyboard expansion

The activity block is anchored after the submitted user task, so the final assistant response remains chronologically after the evidence. The animated progress line still communicates the current step, while the activity block remains visible after completion. Users can focus it with Tab, navigate with Up/Down, and expand with Enter or Space.

## Why

A single mutable spinner hid the evidence needed to understand long coding runs. The canonical agent loop already emits typed lifecycle events, so retaining their application-level projection improves observability without introducing another execution path or provider-specific parsing.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
