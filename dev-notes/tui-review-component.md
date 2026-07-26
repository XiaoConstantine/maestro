# Shared review component

## What changed

Maestro's inline `/review` results and standalone `RunReviewTUI` now host the same `ReviewModel` component. The component owns file grouping, filtering, flattened visible-item selection, list/detail rendering, optional host-supplied post confirmation, and severity presentation. The primary TUI supplies embedded host semantics so component keys cannot quit the whole application; the standalone host retains quit and post behavior.

The duplicate inline review renderer and its parallel selection/expansion state were removed from `MaestroModel`. Review severity now uses shape as well as color (`✖` critical, `●` high, `◆` medium, `○` low), and every rendered line is bounded to the host-provided width.

## Why

Two independent review renderers had already drifted: standalone review supported filters and confirmation while inline review carried a separate three-index selection model. A shared component keeps the specialized review pipeline intact while giving both frontends one presentation and navigation contract.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
