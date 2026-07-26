# Maestro TUI next-stage redesign proposal

> **Historical design snapshot:** this records the worktree immediately before
> Phase 0. References to parallel TUI files and open decisions describe that
> pre-consolidation state; Phase 0 completed their removal and resolved the
> decisions as recorded in `dev-notes/tui-consolidation.md`.

Grounded in the pre-Phase-0 `feat/openai-subscription-login` worktree at
`/tmp/maestro-v086`, after the Bubble Tea v2 refresh
(`dev-notes/tui-refresh.md`). This document was created during the design discussion; no source files were modified during that discussion.

The goal: a coherent next stage that preserves Maestro's dark/coral identity and
its canonical coding / review / session flows, while removing the structural
debt that is already making the refresh hard to reason about.

---

## 0. What the current code actually is

Before redesigning, the single most important finding from inspecting the tree:

**There are two parallel TUI implementations, and only one is live.**

- **Live**: `terminal/maestro_model.go` `MaestroModel` + `input_model.go` +
  `statusbar.go` + `progress.go` + `commands.go` (palette). This is what
  `main.go` → `terminal.RunMaestro` starts. Single-column Crush-style layout:
  logo → info → conversation viewport → progress → input → status bar.
- **Dead**: `terminal/app.go` `Model` (the "modern UI" / Amp-style split pane),
  `terminal/modern.go` (`RunModernUI`/`RunLegacyUI`/`RunUI`, never called),
  `terminal/splitpane.go`, `terminal/filetree.go`, `terminal/todolist.go`, and
  the Vim-style `terminal/keybindings.go` `KeyHandler`/`Mode`/`ActionType`
  system (used only by `app.go`'s `Model.handleAction`).

Verified:
- `grep -rn "RunModernUI|RunUI|NewModern|NewWithOptions"` → no callers outside
  `app.go`/`modern.go`.
- `MaestroModel.keyHandler` is constructed (`maestro_model.go:93`) but never
  read; all key routing is a direct `switch` in `MaestroModel.Update`.
- `terminal/review_keybindings.go` `ReviewKeyHandler`/`ReviewActionType` is
  never instantiated anywhere; `ReviewModel.Update` does its own inline switch.
- `modes.go` defines `ModeReview`/`ModeDashboard` and `ModeTransition`, but no
  code ever sends a `ModeTransition` message (`grep` shows only the handler).
  `m.mode` is effectively always `ModeInput` or `ModeSessionPicker`. The review
  flow is inline-within-input via `ReviewResultMsg`, not a mode transition.

That is roughly **~2,400 lines of dead/parallel code** (`app.go` 771,
`splitpane.go` 278, `filetree.go` 359, `todolist.go` 324, `keybindings.go` 381,
`review_keybindings.go` 269, `modern.go` 40) versus ~1,700 lines of live
`MaestroModel` + ~1,060 of `ReviewModel`.

There is also **duplicated review rendering**: `ReviewModel`
(`review_model.go`, launched standalone via `RunReviewTUI` for the
non-interactive `maestro review <PR>` path) **and** a second hand-rolled inline
renderer inside `MaestroModel` (`renderInlineReview`/`renderReviewList`/
`renderReviewDetail`/`groupCommentsByFile`/severity icons/navigation). The
inline copy is a strict subset: it lacks filters (`0`–`4`), post-to-GitHub
(`p`), search (`/`), skip (`s`), resolve (`r`), and the `ReviewKeyHandler`.
`ReviewComment.CodeBlock`/`DiffBlock` are rendered with plain foreground color.

Other concrete gaps the redesign must address:
- **No markdown rendering** in the live model. `renderMessages` is
  `prefix + lipgloss.Foreground(content)`. `glamour` is a dependency but is only
  used by the dead `app.go` `Model`.
- **Tool activity is a single spinner line** (`progress.go`). The rich
  `agents.ExecutionEvent` taxonomy (`RunStarted`, `TurnStarted`,
  `ToolExecutionStarted`, `ToolCallFinished`, `MessageAdded`, `RunFinished`) is
  flattened in `main.go:mapCodingEvent` to `CodingEvent{Kind,Tool,Status,Detail}`
  and then to one status string. There is no persistent tool-call transcript.
- **No model switching**: the model is fixed at launch via `--model`; the info
  section displays it but you cannot change it in-session.
- **Session picker is rendered inside the conversation viewport** as text, not
  an overlay.
- **Command definitions can drift**: `input_model.go:getBuiltinCommands()` and
  `commands.go:registerDefaultCommands()` are independent lists (palette has
  `tools setup`/`tools status`, inline autocomplete does not).
- **Severity icons are not colorblind-safe**: `getSeverityIcon` maps both
  `critical` and `high` to the same `#FF6B6B` `●` with no shape distinction.

Identity to preserve: `ClaudeCodeTheme()` — background `#1F2041`/`#2B2D42`,
coral logo `#E8985A`, cyan accent `#00D9FF`, mint status `#00FFB2`, purple
keyword `#B185F7`, cyan-green string `#7FE9DE`. Coral is the Maestro signature;
the rest is "Claude Code-inspired / Crush-style" per the comments.

---

## 1. Information architecture

Collapse the vestigial mode enum into **one primary surface with overlays and a
focus model**, because the mode system is already not used as designed.

```
┌─────────────────────────────────────────────┐
│ Header: ◉ MAESTRO  workspace  model  session │
├──────────────────────────────┬──────────────┤
│ Conversation                 │ Context rail │
│  user > …                    │ (tool activ- │
│  ◉ assistant …               │  ity / review│
│  ▶ read foo.go   [collapsed] │  summary /   │
│  ▶ edit bar.go               │  file tree)  │
│  system ⚠ …                  │              │
├──────────────────────────────┴──────────────┤
│ Composer: TASK  enter run • ctrl+j newline  │
│  > _                                         │
├─────────────────────────────────────────────┤
│ status bar: mode • hints                     │
└─────────────────────────────────────────────┘
   Overlays (not modes): command palette, session picker,
   model picker, help, review-detail
```

- **Conversation** — always present. Transcript of user / assistant / system /
  tool-activity / inline-review blocks. Owned by `MaestroModel` (enriched).
- **Composer** — always present. The current `InputModel`, refined.
- **Context rail** — optional right column for wide terminals. Hosts one of:
  tool-activity log (during runs), review summary (when results present), file
  tree (toggle). This is a *region*, not a "dashboard mode." Any future file
  or task component should be rebuilt against this live consumer rather than
  reusing the deleted experimental components.
- **Overlays** — command palette (Ctrl+P), session picker, model picker, help,
  review detail. Rendered via the existing `lipgloss.NewCanvas`/`Compositor`
  compositor already used by `overlayCommandPalette`.

`modes.go`'s `ModeReview`/`ModeDashboard` and the unreachable
`ModeTransition`/`handleModeTransition` path are removed; review becomes a
focus state + embedded component within the conversation, and the standalone
`RunReviewTUI` path shares the same component.

---

## 2. Layout across wide / narrow terminals

Replace the ad-hoc `planInputModeLayout` (which only stacks/drops sections
vertically) with a small `Layout` module that computes rectangular regions for a
given `(width, height, features)` triple. Three breakpoints:

| Breakpoint        | Width        | Rail       | Behavior                                            |
|-------------------|--------------|------------|-----------------------------------------------------|
| Narrow            | < 80 cols    | hidden     | Single column; compact logo; info one line; hints truncate; conversation max |
| Standard          | 80–119 cols  | thin ≤24   | Single column + optional rail (tool activity by default during runs, review summary when results) |
| Wide              | ≥ 120 cols   | 32–40 cols | Conversation + rail side-by-side; rail switchable (tool / review / files) |

Invariants the Layout module must enforce (testable):
1. Conversation height ≥ `minConversationHeightForPane(totalHeight)` (already
   exists; keep it).
2. Sum of region heights/widths never exceeds the terminal (assert
   `lipgloss.Width`/`lipgloss.Height` of the joined view == terminal size).
3. Composer may grow up to ~40% of height as the user types multiline input,
   shrinking conversation; bounded so status bar + ≥3 conversation rows remain.
4. Narrow mode must never render any region at < 1 content column; truncate with
   `ansi.Truncate` (already adopted across the refresh).

The current `splitpane.go` `SplitPaneModel.RenderLayout` does line-by-line
string concatenation with manual padding — fragile and ANSI-unsafe. The new
Layout module should use `lipgloss.JoinHorizontal`/`JoinVertical`/`Place` with
explicit `Width`/`Height` on each region, which is what the refresh already
does for the single-column case.

---

## 3. Conversation & tool rendering

### 3.1 Markdown for assistant messages
Port the glamour renderer from the dead `app.go` `Model` into `MaestroModel`,
configured with:
- `glamour.WithStylePath("dark")` as the base,
- word wrap = conversation content width,
- Maestro syntax colors mapped (`Keyword` `#B185F7`, `String` `#7FE9DE`,
  `Comment` `#6B6B7E`, `Code` `#D4D4E5`).

Cache the rendered string on `Message` (`addMessage` populates `Rendered`).
Re-render only when the width bucket changes (e.g. every 4 cols) to keep resize
cheap. User/system messages stay plain (they are short).

### 3.2 Tool-activity transcript
Introduce a `ToolActivity` model that consumes `CodingEvent` and produces
structured, collapsible entries rendered inline in the conversation:

```
▶ read   foo.go                              120ms
▶ edit   bar.go   (3 lines)                  210ms
▶ write  baz.go                              40ms
— turn 2/8 —
▶ read   bar.go                              95ms
✓ run finished: 3 turns, 5 tool calls, 12.4s
```

- Collapsed by default; expand (Enter in a "tool focus" state, or a dedicated
  key) to show truncated `codingToolDetail` output (already produced in
  `main.go:mapCodingEvent` for `ToolCallFinishedEvent`).
- Turn boundaries render as subtle `— turn n/max —` separators.
- Run finish renders a summary line using `RunFinishedEvent.Turns`/`ToolCalls`
  (currently discarded — `mapCodingEvent` only forwards `Status`/`Diagnostic`).
- The `ProgressModel` spinner stays as the **current-step** indicator, but the
  history now **persists** in the transcript instead of being replaced.

This is the highest-leverage coding-flow improvement: today a long run shows
one spinning line and then a final blob; the user loses all intermediate
evidence. The data already arrives via `CodingEvent`; it is just not retained.

### 3.3 Streaming (optional, gated)
`agents.MessageAddedEvent` exists in dspy-go. To stream assistant text
incrementally instead of waiting for `CodingResultMsg`:
- Extend `CodingEvent` with a `Kind: "message"` / delta payload.
- Forward `MessageAddedEvent` in `main.go:mapCodingEvent`.
- `MaestroModel` appends deltas to the current assistant message block and
  re-renders just that block.

**Tradeoff**: more messages to the TUI; needs batching/throttling (e.g. coalesce
deltas on a ~30ms tick) to avoid starving the UI on fast streams. Gate this
behind a flag; keep the "final answer only" path as default until it is proven.

### 3.4 Message struct
```go
type Message struct {
    Role       string
    Content    string
    Rendered   string    // cached markdown (assistant only)
    ToolEvents []ToolEntry
    Timestamp  time.Time
    WidthBucket int      // bucket at which Rendered was computed
}
```

---

## 4. Input / composer

The current `InputModel` is already solid (Crush-style `> `/`:::` prompt,
Ctrl+J newline, slash autocomplete, history, bounded suggestions). Refinements:

- **Unify command definitions**: a single `CommandRegistry` consumed by both
  `InputModel` autocomplete (`getBuiltinCommands`) and `CommandPaletteModel`
  (`registerDefaultCommands`). Today they can drift. Add a test that asserts
  parity.
- **Composer grows with content**: up to ~40% of terminal height as lines are
  added, shrinking the conversation region via the Layout module. Currently
  fixed at 3 rows (`inputTextareaHeightForPane`).
- **Line/char indicator**: when > 1 line, show `3 lines · 480 chars` so users
  remember Enter submits (not newline).
- **Draft preservation**: persist the current draft on the session so switching
  sessions or an accidental Esc does not lose input. Esc currently cancels
  runs; ensure a non-empty draft survives unless Esc is pressed twice
  (first Esc cancels run / closes overlay, second Esc clears draft — confirm
  this UX with you; see decisions).
- **`@path` file references** (optional): expand `@path/to/file` to include
  file contents in the prompt. Requires backend support; nice for coding agents
  but out of scope unless you want it.

---

## 5. Command / model / session navigation

### Commands
- `CommandRegistry` (single source) feeds both inline autocomplete and palette.
- Palette overlay already uses the canvas compositor; keep it.

### Model picker (new)
- `Ctrl+M` (or `/model`) opens a palette-style overlay listing configured
  provider/model combos from the backend.
- Requires extending `MaestroBackend` with `ListModels() []ModelOption` and
  `SetModel(provider, model string) error`.
- **Tradeoff**: hot model switching mid-session is convenient but changes
  context/cost. Decide whether it applies to the *next* run only or also
  reconfigures the active agent immediately. (See decisions.)

### Session picker → overlay
- Promote the session list out of the conversation viewport
  (`renderSessionPicker`) into a real overlay component, reusing the palette's
  compositor and j/k/enter/fuzzy-filter interaction.
- This stops polluting the transcript and gives consistent navigation.
- Add session name to the header info section so context is always visible
  (currently it only appears in `/help` output).

---

## 6. Review UX

Unify the two implementations on **one `ReviewComponent`**:

| Feature            | Inline (MaestroModel) | Standalone (ReviewModel) | Unified |
|--------------------|:-----:|:-----:|:-----:|
| File grouping      | ✓     | ✓     | merge |
| Expand/collapse    | ✓     | ✓     | merge |
| List + detail split| ✓     | ✓     | merge |
| Severity filters   | ✗     | ✓ (0–4)| add to inline |
| Post to GitHub (p) | ✗     | ✓     | wire `onPost` into inline |
| Search (/)         | ✗     | ✓     | add |
| Skip (s)/Resolve(r)| ✗     | ✓     | add |
| Help (?)           | ✗     | (keymap) | add |
| Diff/code styling  | plain | plain | glamour + theme colors |

Concrete steps:
- Extract `ReviewComponent` (state + render) from the two implementations. The
  inline path embeds it in the conversation region; `RunReviewTUI` wraps it in
  its own program for the non-interactive `maestro review <PR>` flow.
- Build one review keymap inside the shared component, covering filters,
  search, post, skip, resolve, help, and metrics.
- Fix the **dual-index selection** bug-prone scheme (`selectedReviewIdx` +
  `selectedFileIdx` + `selectedCommentIdx` + `updateFileIndexFromReviewIdx`,
  duplicated in both files). Replace with a single flattened visible-items
  index + a helper mapping to `(fileIdx, commentIdx)`.
- Render `ReviewComment.CodeBlock`/`DiffBlock` with the same glamour/code
  styling as conversation (diff: `+` lines `#7FE9DE`, `-` lines `#FF6B6B` —
  already the convention in `ReviewStyles`).
- Wire `onPost` into the inline path: `ReviewResultMsg` currently does not
  carry a post callback. Either include it in the message or look it up from
  the backend (extend `MaestroBackend` with a review-post capability, or pass
  the callback through `cmdReview`).

---

## 7. Accessibility

- **Colorblind-safe severity**: today `critical` and `high` are both `#FF6B6B`
  `●` — indistinguishable. Add **shape**: critical = `✖` (or `▲`), high = `●`,
  medium = `◆`, low = `○`. Keep color as a secondary cue.
- **Reduced motion**: detect `NO_COLOR` env / a `--no-spin` flag; `ProgressModel`
  then renders a static `[working] elapsed` label instead of the animated
  braille spinner. (`renderWorking` always animates today.)
- **High-contrast theme variant**: a `HighContrastTheme()` that bumps
  `Border` from `#3A3C55` → e.g. `#5A5C75` and `TextMuted` up a step. Selectable
  via flag/env.
- **Keyboard completeness**: every action is already keyboard-reachable
  (scroll via j/k, ctrl+d/u). Keep the invariant that every overlay is
  dismissible with Esc.
- **Minimum-width safety**: keep the refresh's `ansi.Truncate` discipline and
  the `planInputModeLayout` degradation strategy (drop logo → info → progress →
  status to protect conversation). Encode as Layout invariants with tests.
- **Non-TUI export**: out of scope for the TUI itself, but note that a
  `maestro --print` transcript mode is the screen-reader-friendly fallback.

---

## 8. Incremental implementation & testing plan

Each phase builds green and is independently mergeable. No phase changes
orchestration or dspy-go contracts; all work stays in `terminal/` (+ a small
backend interface addition in Phase 5).

### Phase 0 — Consolidate & delete dead code (no UX change)
- Remove `app.go` `Model` + methods, `modern.go`, `keybindings.go`,
  `review_keybindings.go`, `splitpane.go`, `filetree.go`, and `todolist.go`.
  Git history is the archive; future components should be rebuilt against a
  named live consumer rather than retaining parallel implementations.
- Remove the unreachable `ModeTransition`/`handleModeTransition`/`ModeDashboard`
  path; simplify `modes.go` to `ModeInput` + `ModeSessionPicker` (or replace
  modes with a focus enum).
- Introduce `CommandRegistry`; point both `InputModel` and `CommandPaletteModel`
  at it.
- **Tests**: existing tests pass; add a registry parity test
  (`getBuiltinCommands` == palette registered commands); add a test that
  `maestro_model.go` no longer references removed symbols.

### Phase 1 — Markdown + message enrichment (visible, low risk)
- Add a cached glamour renderer to `MaestroModel`; render assistant messages
  with Maestro syntax colors.
- Enrich `Message` with `Rendered`/`Timestamp`/`WidthBucket`.
- Reflow on width-bucket change only.
- **Tests**: `renderMessage` on known markdown input produces expected
  substring(s); re-render at same width returns cached (identity); width-bucket
  change re-renders.

### Phase 2 — Tool-activity transcript (core coding-flow win)
- New `ToolActivity` model consuming `CodingEvent`; collapsible entries in the
  conversation; turn separators; run summary from `RunFinishedEvent`.
- Keep `ProgressModel` spinner for the current step only.
- (Optional, gated) extend `mapCodingEvent` to forward `MessageAdded` for
  streaming.
- **Tests**: feed a synthetic `CodingEvent` sequence, assert rendered lines and
  expand/collapse states; cancel mid-run clears spinner but retains history;
  run-summary line includes turns/tool-calls.

### Phase 3 — Layout + context rail (wide terminals)
- New `Layout` module computing regions; wire into `renderInputMode`.
- Context rail (right column): tool activity during runs and review summary
  when results are present. A future file tree requires a new interaction
  contract and is not part of this phase.
- Breakpoints narrow/standard/wide; rail collapsible (e.g. `Ctrl+\`).
- **Tests**: parametric table over `(width,height)` → expected region rects;
  assert conversation min-height invariant; assert joined view dimensions ==
  terminal (no overflow); narrow mode produces no rail.

### Phase 4 — Review unification
- Extract `ReviewComponent` from both implementations; inline embeds it,
  `RunReviewTUI` wraps it.
- Build the component's shared keymap (filters, post, search, skip, resolve, help).
- Single flattened-index selection model.
- Glamour-styled code/diff blocks.
- Colorblind-safe severity shapes.
- Wire `onPost` into the inline path.
- **Tests**: port `review_model_test.go` + the review-counts tests in
  `maestro_model_test.go` to the component; add filter/post/search interaction
  tests; add severity-shape assertion.

### Phase 5 — Model picker + session overlay
- Extend `MaestroBackend` with `ListModels()`/`SetModel(...)` (NoOp returns
  empty / no-op).
- `Ctrl+M` model picker overlay (reuse palette compositor).
- Session picker as overlay (reuse palette compositor); add session name to
  header.
- **Tests**: NoOp `ListModels` empty; picker renders + navigates; model select
  dispatches `SetModel`; session switch cmd dispatches backend call.

### Phase 6 — Accessibility polish
- Reduced-motion mode (static progress label).
- `HighContrastTheme()` variant + flag/env.
- Severity shapes (partly done in Phase 4).
- **Tests**: reduced-motion returns static string; high-contrast theme renders
  distinct border color.

Verification at every phase (from `dev-notes/tui-refresh.md`):
```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./terminal
GOWORK=off go vet ./...
golangci-lint run ./...
GOWORK=off go mod tidy -diff
git diff --check
```

---

## 9. Historical decisions resolved before Phase 0

The implementation direction is: hard-delete the experimental parallel TUI;
keep standalone review as a thin host around a future shared component; defer
streaming and `@path`; apply model changes to the next idle run; never clear a
draft with Esc; use an adaptive, user-dismissable rail; cap composer growth at
30% or 10 rows; expose theme and reduced-motion controls through flags and
environment variables; and rebuild any future file view against a live
interaction contract.

The original discussion questions follow for design-history context:

1. **Dead code: delete or archive?** Confirmed dead: `app.go` `Model`,
   `modern.go`, `keybindings.go` Vim system, `review_keybindings.go`,
   `splitpane.go`. Reusable: `filetree.go`, `todolist.go` (for the rail). Do you
   want a hard delete, or a `terminal/legacy/` subpackage archive for one cycle?

2. **Review mode: inline-only, or keep standalone `RunReviewTUI`?** The
   non-interactive `maestro review <PR>` path (`review_tui_bridge.go` →
   `RunReviewTUI`) is a separate entry point. Unify on `ReviewComponent` — but
   do you want the standalone full-screen review to remain a distinct
   invocation, or fold review entirely into the main `RunMaestro` TUI?

3. **Streaming assistant text (Phase 3.3)?** It requires extending
   `CodingEvent` + `mapCodingEvent` and a throttle. Do you want it now, or defer
   and keep "final answer only" to limit chatter/risk?

4. **Model switching semantics.** When the user picks a new model via `Ctrl+M`,
   should it apply to the *next* run only (safe, no mid-run reconfigure), or
   reconfigure the active agent immediately (flexible but semantically messy
   mid-turn)? And does model switching require a session restart to keep
   transcript/accounting coherent?

5. **Esc semantics + draft preservation.** Today Esc cancels a run or closes an
   overlay. Should a non-empty composer draft survive Esc? Proposal: first Esc
   cancels run / closes overlay and *keeps* the draft; a second Esc (when idle)
   clears the draft. Agree?

6. **Context rail default content.** For wide terminals, should the rail default
   to (a) tool-activity during runs + review summary when results exist, (b)
   always file tree, or (c) hidden until explicitly toggled? My recommendation
   is (a).

7. **Composer auto-grow.** Letting the composer expand up to ~40% of height as
   the user types multiline input shrinks the conversation. Is that desirable,
   or do you prefer a fixed-height composer with internal scroll?

8. **High-contrast / reduced-motion discovery.** Should these be flags
   (`--high-contrast`, `--no-spin`), env vars (`MAESTRO_HIGH_CONTRAST=1`,
   `NO_COLOR`), or both? My recommendation: honor `NO_COLOR` for reduced motion,
   add `--high-contrast` flag + `MAESTRO_THEME=high-contrast` env.

9. **`@path` file-reference expansion in the composer.** Nice for coding agents
   but needs backend help (read file, inject into prompt). In scope for this
   redesign or a later follow-up?

10. **Dashboard / file-tree end state.** Is the file tree intended to be a
    first-class navigable pane (open files, trigger `/ask` about a file), or
    just a passive context display in the rail? This affects how much we invest
    in `filetree.go` (it currently has no open-file action wired).

---

## Summary

The refresh in this worktree is a good, correctly-scoped dependency + UX polish
pass. The next stage's biggest structural win is **collapsing the dual TUI
implementation and the dual review renderer into one**, then spending the
freed-up clarity budget on **a real tool-activity transcript** (the data already
arrives via `CodingEvent`), **markdown rendering** (glamour is already a dep),
and a **responsive layout with a context rail** for wide terminals — all behind
the existing dark/coral theme and the canonical coding/review/session flows.
