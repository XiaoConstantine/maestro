# Session and model picker overlays

## What changed

Session selection is now composed as an overlay and no longer becomes conversation content. The responsive header displays workspace, model, and session together.

A small optional `modelSelectionProvider` interface lets a Maestro backend list configured `ModelOption` values and apply a selected model. Ctrl+M and `/model` are advertised only when that capability exists. Users may browse while a coding run is active, but Enter rejects selection until the run ends; successful selection applies to the next run without silently restarting the session.

Model and session mutations reserve one correlated request before launching asynchronous work. Coding-run admission and additional mutations are blocked until it completes, stale result messages are ignored, and session changes are rejected during active coding or specialist `/ask`/review/subagent work. A confirmed session switch resets session-scoped visible messages, tool activity, and review state. Subagent tools and session ids are snapshotted under the service read lock before execution, matching the writer lock used by session switching.

Both pickers share the ANSI-safe canvas compositor, terminal-height scrolling windows, arrow navigation, Enter selection, and Esc dismissal. Typing applies case-insensitive subsequence filtering across session names or model IDs/descriptions; Backspace edits the query and Ctrl+U clears it. Pickers are modal: unrelated keys are consumed and Ctrl+P cannot place a command palette above them. Overlays close before Esc is interpreted as coding-run cancellation.

## Why

Rendering session choices inside the viewport polluted durable transcript state. Overlay composition keeps transient navigation separate from messages. A narrow optional model capability avoids reflection and prevents the core backend interface from requiring model switching where it cannot be supported safely.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
