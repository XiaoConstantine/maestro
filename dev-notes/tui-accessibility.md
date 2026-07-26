# TUI accessibility controls

Phase 6 adds explicit presentation controls without changing Maestro's dark/coral default identity.

- `--high-contrast` and `MAESTRO_THEME=high-contrast` select stronger muted-text and border colors.
- `--reduce-motion` and `MAESTRO_REDUCE_MOTION=1` replace the animated spinner with a static `[working]` label while retaining periodic elapsed-time updates.
- `NO_COLOR` selects a color-free theme and leaves reduced motion independent. Structure, text emphasis, review severity shapes, and status labels remain available without hue.

These settings are resolved when the canonical `MaestroModel` is constructed, so embedders receive the same environment behavior as the CLI. Tests cover color-free Markdown, environment theme selection, and stable reduced-motion frames.

Run:

```sh
GOWORK=off go test ./terminal
GOWORK=off go test -race ./terminal
```
