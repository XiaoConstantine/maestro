# Maestro Coding Session

Maestro's interactive default is a workspace coding agent. Natural-language TUI input runs a provider-neutral `dspy-go` native agent against one authoritative workspace root; `/ask` remains the read-only repository-QA route.

## Architecture

```text
terminal.MaestroModel
  -> terminal.MaestroBackend
  -> orchestration.MaestroService
  -> internal/coding.Session
  -> native.Agent.ExecuteWithTrace
```

The boundaries mirror Tau/Pi:

- `dspy-go/pkg/agents` owns the reusable execution loop, typed events, messages, and canonical traces.
- `internal/coding.Session` owns Maestro's workspace tools, active-run cancellation, and session persistence configuration.
- `internal/orchestration` selects one authoritative workspace, supplies explicit Maestro coding instructions, and records trace usage.
- `terminal` maps typed lifecycle events into Bubble Tea display progress.

The frontend does not inspect provider responses or parse result maps.

## Behavior

A coding session registers these workspace-contained file tools by default:

- `ls`
- `read`
- `write`
- `edit`

When GitHub review services are configured, it also registers `review_pull_request` through dspy-go's `pkg/agents/subagent` adapter. `/review <PR>` and that delegated tool invoke the same specialized Maestro review pipeline; no second provider-native execution loop is introduced.

File tools reject paths outside the authoritative workspace root. Shell execution is disabled by default because setting a working directory is not a sandbox: shell commands can otherwise access absolute paths, `$HOME`, and the network. Pass `--allow-coding-bash` to opt into unrestricted `bash` with the workspace as its initial working directory.

The coding agent receives explicit provider/model/workspace instructions and evidence rules: after any `write` or `edit`, it must verify the mutation with tool evidence before calling `Finish`. Final answers should describe only mutations that were actually observed in the workspace trace.

One run may mutate a session at a time. Overlapping prompts are rejected. Press **Esc** to cancel the active coding run. Run, turn, and tool lifecycle events update the TUI progress display. The contextual status bar keeps cancellation and navigation actions visible without overwhelming narrow terminals. Output and accounting use the operation-scoped `ExecutionTrace` returned by `ExecuteWithTrace`, avoiding races against a mutable last-trace accessor.

The active Maestro session name selects the native session id, so switching or creating a session selects separate persisted coding history.

## Running

```bash
go run . --interactive \
  --owner XiaoConstantine \
  --repo dspy-go \
  --model google:gemini-2.5-flash
```

Add `--allow-coding-bash` only when you trust the active model and accept unrestricted shell access.

Enter a task such as:

```text
Inspect the failing tests, implement the smallest fix, and run the focused tests.
```

In interactive mode, the workspace is the current working directory you launched Maestro from. The responsive header labels that workspace and the active provider/model; coding tools and any file-tree view should all refer to the same root.

Prompt controls:

- **Enter** runs the current task.
- **Ctrl+J** inserts a newline without submitting.
- **/** displays matching slash commands inline.
- **Ctrl+P** opens the searchable command palette.
- **Up/Down** navigates command suggestions or prompt history.
- **Tab** switches focus between inline review results and the prompt.
- **Esc** cancels an active coding run or closes the current picker/detail view.

For a non-interactive smoke test:

```bash
go run ./cmd/maestro-probe \
  --strategy coding \
  --repo-path /path/to/repository \
  --question "Create hello.txt and verify its contents" \
  --model google:gemini-2.5-flash
```

## Current boundary

This slice establishes the coding-session execution spine. Tau-level steering/follow-up queues, compaction, session-tree transcript rendering, project instruction discovery, skill/prompt pickers, model switching, and token-delta streaming remain follow-up frontend/session features. They should be added above `dspy-go`'s canonical contracts rather than by creating another execution loop.
