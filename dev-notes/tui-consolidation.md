# Canonical TUI consolidation

## What changed

Maestro now has one primary coding-TUI path: `main.go` starts `terminal.RunMaestro`, which owns `MaestroModel` and its focused components. The unused parallel `Model`/modern/legacy entry points, Vim mode router, split pane, file tree, todo list, and disconnected review key router were removed. The live standalone `RunReviewTUI` entry point remains and will become a thin host around the shared review component in the review-unification phase.

The unreachable dashboard/review mode-transition path was also removed. The remaining top-level interaction state is the coding surface plus the session picker; inline review selection is represented as focus within that surface.

Slash-command autocomplete and the command palette now consume `builtinCommands`, one canonical registry. Unsupported palette-only tool commands were removed, while `/clear` is now discoverable in both surfaces.

## Why

The parallel coding TUI was not reachable from Maestro's CLI and made UI changes appear to work in components users never saw. Keeping one primary coding rendering and event-routing path gives subsequent redesign phases a reliable base. Git history remains the archive for deleted experiments.

## Compatibility

This is an intentional pre-v1 cleanup of the exported `terminal` package. External users of the experimental `Model`, `New`, `NewModern`, `RunUI`, `RunModernUI`, `RunLegacyUI`, file-tree, todo, split-pane, or key-handler APIs must migrate to `RunMaestro` for interactive coding or `RunReviewTUI` for standalone review. The unused `Command.Handler` and `CommandPaletteModel.SetHandler` hooks were also removed; command execution belongs to `MaestroModel.handleCommand`. `ModeSessionPicker` retains its historical numeric value.

The shared command registry prevents autocomplete and the palette from drifting or advertising commands the active router cannot execute.

## Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```
