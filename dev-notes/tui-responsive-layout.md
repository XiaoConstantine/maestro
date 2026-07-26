# Responsive TUI layout and context rail

## What changed

The canonical TUI now separates vertical chrome planning from horizontal conversation planning.

- Below 80 columns, Maestro remains a single chronological conversation.
- From 80–119 columns, requested run/review context uses a 24-column rail.
- At 120+ columns, the rail scales from 32 to 40 columns.
- The rail request survives temporary terminal narrowing and can be toggled with Ctrl+\\ without deleting activity or review state.
- Tool selection uses the rail viewport when visible and the conversation viewport when inline.

The composer now grows with multiline content while respecting `min(10 rows, 30% of terminal height)`. It displays line and character counts for multiline drafts.

## Why

A fixed single column wastes wide terminals, while always-visible sidebars crowd narrow ones and cause unnecessary transcript reflow. The adaptive rail appears only after canonical run or review context exists, stays reserved until dismissed, and degrades to the existing inline transcript at narrow widths.

The composer cap supports detailed prompts without sacrificing the minimum conversation region.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
