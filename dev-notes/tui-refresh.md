# TUI dependency and UX refresh

## What changed

Maestro now uses stable Bubble Tea v2 releases instead of release candidates and pseudo-versions:

- Bubble Tea `v2.0.8`
- Bubbles `v2.1.1`
- Lip Gloss `v2.0.5`
- Glamour `v2.0.1` under its canonical `charm.land` module path
- Charm ANSI `v0.11.7`

The focused UX pass preserves Maestro's existing dark/coral visual language while making the primary coding flow clearer:

- responsive workspace/model context
- context-sensitive status and keyboard hints
- a searchable, correctly sized command palette
- bounded inline command suggestions
- explicit prompt controls, including Ctrl+J for multiline input
- safer rendering at narrow terminal widths

## Why

The old dependency set predated the stable Bubble Tea v2 ecosystem. The UI also hid active status messages, did not size the command palette until it was visible, and presented several static shortcuts that did not match the current coding-session interaction model.

The refresh keeps presentation policy in `terminal`; it does not change Maestro's orchestration or dspy-go's reusable execution layer.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
