# TUI Markdown rendering

## What changed

The canonical `MaestroModel` now renders assistant messages with Glamour using Maestro's existing dark/coral palette. Headings, links, inline code, fenced code, comments, keywords, and strings receive terminal-aware styling.

`Message` stores the original content, rendered output, rendering width bucket, and timestamp. Assistant output is rendered lazily and cached. Crossing a four-column width bucket triggers reflow; repeated progress updates and one-to-three-column resize changes reuse the cached block.

User and system messages remain plain, role-specific terminal text.

## Why

Provider responses commonly contain Markdown and code. The previous live TUI displayed the syntax markers literally even though Glamour had once been wired only into the deleted experimental interface. Moving rendering into the canonical conversation path improves readability without changing provider or agent execution.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
