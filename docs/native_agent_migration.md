# Maestro Native Agent Migration Spec

## Executive Summary

Maestro's interactive architecture has fallen behind `dspy-go`'s current native runtime. The review stack is still specialized and worth preserving, but the interactive and QA paths should stop centering ReAct and file-backed markdown sessions.

This spec proposes a staged migration:

1. Keep the PR review engine and RLM review wrapper specialized.
2. Replace Maestro's interactive `/ask` and future coding flows with `dspy-go`'s native tool-calling agent.
3. Replace Maestro's file-based `context.md` session model with `sessionevent` SQLite sessions and branchable lineage.
4. Standardize the default interactive tool surface around the new minimal pack:
   - `ls`
   - `read`
   - `write`
   - `edit`
   - `bash`
   - `finish` (provided by the native runtime)

The goal is not a full rewrite. The goal is to modernize the general interactive path while leaving the PR review path intact.

---

## Current State

### 1. Persistence Is Not Real Yet

`internal/orchestration/service.go` exposes a `MemorySQLite` option, but it still falls back to `agents.NewInMemoryStore()`.

Implication:
- Maestro presents a persistence option that does not actually persist interactive agent state.

### 2. QA Is Not Actually Persistent

`internal/orchestration/pool.go` caches a `QAAgent`, but each `Ask()` call constructs a fresh ReAct agent via `createReActAgent(...)`.

Implication:
- `/ask` does not behave like a real long-lived assistant.
- Session continuity is reconstructed indirectly rather than preserved by runtime state.

### 3. Maestro Currently Has Two Different Session Concepts

Maestro currently mixes two distinct mechanisms:

1. `internal/subagent/session.go`
   - file-based session directories plus `context.md`
   - used as a file exchange channel for Claude/Gemini processors

2. `agents.Memory` plus `agents.SessionStore`
   - used for QA/native-style state and recall

Implication:
- the file-based session manager is not the same thing as Maestro's interactive state model
- replacing them needs different timelines
- the file-based channel may need to remain longer for legacy subprocess integrations even after interactive state moves to `sessionevent`

### 4. Persistence Is Still Wired to In-Memory Storage

`internal/orchestration/service.go` exposes a `MemorySQLite` option, but it still falls back to `agents.NewInMemoryStore()`.

`dspy-go` already has `pkg/agents/memory/sqlite_memory.go`, but Maestro does not currently use it.

Implication:
- Maestro presents a persistence option that does not actually persist interactive state
- the migration needs to decide whether `agents.Memory` consumers move to `memory.SQLiteStore` temporarily, or whether `sessionevent` fully supersedes that path for interactive use

### 5. ReAct Is Still the Default Interactive Runtime

`internal/agent/react.go` builds Maestro's core QA/search behavior on `react.ReActAgent`.

Implication:
- Maestro is still optimized around an older runtime model
- runtime control, sessioning, and tool execution are less aligned with current `dspy-go`

### 6. `/ask` Is a Specialized Search Runtime Today

Maestro's current `/ask` path is not just "a ReAct agent":

- it binds `search.SimpleSearchTool` to the active repo path
- it exposes search-oriented tools such as:
  - `search_files`
  - `search_content`
  - `semantic_search`
  - `read_file`
- `UnifiedReActAgent` also carries Maestro-specific search/runtime behavior:
  - query analysis
  - search-context tracking
  - quality tracking
  - context manager integration
  - ACE hooks
  - flight recorder support

Implication:
- moving `/ask` to the native runtime is not a pure agent swap
- the spec must distinguish:
  - what search capability must be preserved in phase 3
  - what higher-order ReAct-specific instrumentation is intentionally deferred

---

## Goals

### Primary Goals

1. Make Maestro's interactive runtime native-agent-first.
2. Make session persistence real and branchable.
3. Keep the default tool surface minimal and legible.
4. Preserve the existing review engine while upgrading the interactive path.

### Secondary Goals

1. Reduce bespoke Maestro session code.
2. Reuse the new `dspy-go` defaults pack and session stack directly.
3. Prepare Maestro for later interrupt, delegation, and higher-level coding flows.

### Non-Goals

1. Do not rewrite the specialized PR review engine in phase 1.
2. Do not replace the RLM review wrapper in phase 1.
3. Do not introduce a large Hermes-style built-in tool catalog.
4. Do not add search/FTS/session-compaction features to Maestro before the basic migration works.

---

## Design Principles

### 1. Native First

For general interactive work, Maestro should use the `dspy-go` native tool-calling agent rather than ReAct.

### 2. Minimal Default Tool Surface

Adopt the Pi-style default posture:

- `ls`
- `read`
- `write`
- `edit`
- `bash`

Everything else should be explicit extension, not default baggage.

### 3. Sessions Are First-Class Product State

The canonical interactive session should be a branchable SQLite-backed sessionevent store, not `context.md`.

### 4. Preserve the Review Stack

Maestro's review engine is specialized enough that it should be preserved while the interactive path is modernized.

---

## Proposed Architecture

### Split the Product Into Two Runtime Lanes

#### Lane A: Specialized Review Runtime

Keep the current review path:

- `internal/review/*`
- `internal/orchestration/service.go` review handling
- `internal/rlm/review_wrapper.go`

This lane remains review-specific and does not need to become a generic coding agent immediately.

#### Lane B: Native Interactive Runtime

Replace the current QA/session/subagent interactive path with:

- `dspy-go/pkg/agents/native`
- `dspy-go/pkg/agents/sessionevent`
- `dspy-go/pkg/tools/defaults`

This lane becomes the default for:

- `/ask`
- future coding tasks
- future file-editing tasks
- future branch/session exploration in the TUI

### `/ask` Migration Strategy

This migration takes option **B**, not option **A**:

- **A** would simplify `/ask` from a search-specialized path into a generic coding agent and accept temporary search regression
- **B** preserves Maestro's search strengths by adding Maestro search tools on top of the native runtime

This spec chooses **B**.

Phase 3 should preserve search capability by registering a small Maestro search extension pack alongside the Pi-style defaults.

What is preserved in Phase 3:

- repo-aware search tools
- repo-rooted tool confinement
- session persistence
- native lifecycle events

What is intentionally deferred after Phase 3:

- QueryAnalyzer parity
- SearchContext timeline tracking
- QualityTracker scoring
- Maestro ACE hooks on the interactive path
- flight-recorder integration for the native runtime wrapper

Those should be treated as follow-up reintegration work, not silently assumed to come "for free" from the native runtime.

### MaestroBackend Is the Real UI Migration Surface

For the TUI, the primary integration contract is not `ProcessRequest(...)` directly. It is `terminal.MaestroBackend`.

Today the UI depends on:

- `AskQuestion(ctx, question) (string, error)`
- `CreateSession(ctx, name) error`
- `SwitchSession(ctx, name) error`
- `ListSessions(ctx) ([]SessionInfo, error)`
- `GetCurrentSession() string`

This means the native runtime wrapper must either:

1. implement `MaestroBackend` directly, or
2. sit behind a thin adapter that satisfies `MaestroBackend`

This spec assumes option 2:

- `internal/native` provides the runtime
- `MaestroService` and/or the existing TUI adapter flatten runtime results into the current backend interface for phase 3

The backend interface can evolve later if Maestro decides to surface richer structured responses directly.

---

## Proposed Maestro Components

### 1. Native Session Runtime

Add a Maestro-native runtime wrapper around:

- `native.Agent`
- `sessionevent.SQLiteStore`
- `defaults.Toolset`

Suggested package:

- `internal/native`

Suggested responsibilities:

- construct the native agent
- open the sessionevent SQLite store
- resolve current session + active branch
- own a configured default workspace root independent of the review agent
- expose small session control operations
- return structured response data for TUI/CLI integration
- emit native lifecycle events upward for UI streaming
- support explicit shutdown

Suggested API shape:

```go
type Runtime struct {
    llm          core.LLM
    tools        []core.Tool
    sessionStore sessionevent.SessionEventStore
    config       RuntimeConfig
}

type RunRequest struct {
    Task          string
    SessionID     string
    BranchID      string
    WorkspaceRoot string
}

type RunResponse struct {
    FinalAnswer string
    Completed   bool
    Trace       *agents.ExecutionTrace
    TraceSummary RuntimeTraceSummary
    Session     SessionState
}

type RuntimeTraceSummary struct {
    Turns     int
    ToolCalls int
    LastTool  string
}
```

This should be Maestro's canonical general-purpose runtime.

The wrapper should not expose `native`-internal trace types as Maestro's public runtime contract. Returning `agents.ExecutionTrace` plus a UI-friendly summary is a safer boundary.

Concurrency model:

- the wrapper should not share one `native.Agent` instance across concurrent requests
- it should create a fresh `native.Agent` per run using shared config, tool registration, and session store

Reason:

- `native.Agent` carries per-run trace state
- Maestro may later support background tasks or overlapping UI requests
- per-run agent construction is simpler than introducing shared-agent serialization semantics

Lifecycle model:

- `Runtime.Close()` should close the underlying SQLite store
- `MaestroService.Shutdown()` should call `Runtime.Close()`

This is required so the event-store backend flushes cleanly on shutdown.

Workspace root resolution must also be explicit. The native runtime needs a concrete root for tool confinement.

Resolution order:

1. `RunRequest.WorkspaceRoot` if explicitly provided
2. configured default repo root from Maestro runtime config
3. active cloned repo path from the review pipeline if explicitly available and desired
4. current working directory for purely local interactive use
5. fail fast if none of the above can be resolved

Important boundary:

- the native runtime must not depend on the review agent to function
- review-agent repo paths are optional hints, not the primary workspace source

### 2. Native Session Store Wiring

Use `sessionevent` SQLite directly instead of Maestro's current markdown session directories.

Suggested storage location:

- `<maestro state dir>/session.db`

Suggested migration rule:

- keep `internal/subagent/session.go` temporarily only for legacy Claude/Gemini file exchange
- stop treating it as the canonical session mechanism

Session lifecycle must be explicit. The runtime wrapper should implement a create-or-get flow:

1. resolve requested `session_id`
2. call `GetSession(sessionID)`
3. if missing, call `CreateSession(...)`
4. use the returned default branch as the active branch
5. only then resolve branch selection, forking, or lineage loading

This bootstrap must happen before branch resolution or entry append.

This is important because the current `dspy-go` native path only auto-creates a session lazily during persist. That is too late for Maestro, since branch operations such as fork and switch need the session to exist before the first run completes.

### 3. Session Lifecycle and Dual-Write Transition

During migration, Maestro must avoid running two different interactive persistence models indefinitely.

Current `dspy-go` native behavior still supports:

- snapshot-style session persistence through `SessionStore`
- event-store persistence through `sessionevent`

Maestro should define a clear cutover:

1. phase-in period: sessionevent is introduced and validated
2. canonical switch: sessionevent becomes the only authoritative interactive session store
3. compatibility fallback: snapshot recall is no longer used for normal Maestro interactive flows

Do not leave the fallback path as silent long-term behavior, or Maestro will accumulate ghost state between:

- in-memory/snapshot recall
- SQLite sessionevent recall

### 4. Minimal Default Tool Pack

Use `dspy-go/pkg/tools/defaults` as the base interactive tool surface.

This gives Maestro:

- file inspection
- file edits
- shell execution
- workspace confinement
- minimal default surface

Do not introduce a broad extra tool catalog in phase 1.

The one intentional tradeoff is search:

- the minimal pack does not include a dedicated grep/search tool
- the runtime can still search through `bash`

For Maestro specifically, the right compromise is:

- keep the Pi-style defaults as the base pack
- add a small Maestro search extension pack on top for `/ask`

Suggested extension tools:

- `find_files`
- `search_content`
- `semantic_search`

This should be implemented as an adapter over Maestro's existing search stack where possible, not as a shell-script substitute. The current `search.SimpleSearchTool` and sgrep-backed paths are better aligned with Maestro's `/ask` behavior than forcing the native runtime to rediscover search through `bash`.

This preserves Maestro's current search-heavy strengths without turning the default runtime into a giant tool registry. If the extension pack is temporarily unavailable, `bash` remains the fallback.

The read/write/edit/bash defaults also have important limitations:

- `bash` runs with an intentionally filtered environment
- `read` is full-file and model-truncated, not line-range-aware

Those are acceptable defaults, but Maestro should treat them as integration constraints and not assume a rich IDE-like file API on day one.

### 5. Branch Semantics

Sessionevent branches are lineage-based, not copy-based.

Fork behavior is effectively copy-on-write:

- the fork points at an existing ancestor entry
- older entries are shared through `parent_id` lineage traversal
- new entries are appended on the new branch only

Implication for Maestro UI:

- branch-scoped counts based only on `branch_id` will be misleading
- branch views should be rendered from lineage traversal, not naive branch-local entry counting

### 6. TUI Session Controls

Expose native session controls in the terminal UI:

- show current session
- show current branch
- switch branch
- fork branch

This should be built on native session operations rather than custom Maestro session file logic.

This is mostly a re-wiring task, not a net-new UI feature. Maestro already has:

- `session new`
- `session switch`
- `session list`

registered in the TUI command layer. The migration should reuse those commands and change their backend implementation from file-based subagent sessions to native/sessionevent-backed sessions.

### 7. Backend Response Strategy

The native runtime naturally produces richer output than the current TUI backend interface.

Current backend contract:

- `AskQuestion(ctx, question) (string, error)`

Native runtime contract:

- final answer
- completion status
- trace
- session state
- event stream

Phase 3 decision:

- keep `MaestroBackend.AskQuestion(ctx, question) (string, error)` for compatibility
- flatten `RunResponse` to string output at the TUI adapter boundary
- keep structured runtime data internal for:
  - progress/event streaming
  - later interface evolution
  - debugging and metrics

This avoids a broad UI interface change during the first migration.

---

## File-Level Migration Plan

### Phase 1: Make Persistence Honest

#### Target Files

- `internal/orchestration/service.go`

#### Changes

1. Replace the fake `MemorySQLite` behavior with real persistent state wiring.
2. Decide whether remaining `agents.Memory` consumers use `memory.SQLiteStore` temporarily or remain explicitly non-canonical.
3. Initialize a `sessionevent` SQLite store under the Maestro state directory.
4. Stop claiming SQLite memory support if the native session store is not active.
5. Define the canonical session ID source for interactive Maestro sessions.
6. Define when snapshot dual-write is disabled for Maestro interactive flows.
7. Define a default workspace root source for the native runtime that does not depend on the review agent.

#### Acceptance Criteria

- Maestro no longer silently falls back to `agents.NewInMemoryStore()` when SQLite is requested.
- A real session database exists on disk and is used by the interactive runtime.

---

### Phase 2: Add Native Runtime Wrapper

#### Target Files

- `internal/native/runtime.go` (new, required)
- optional supporting files as implementation dictates

#### Changes

1. Add a thin Maestro wrapper around `native.Agent`.
2. Register `defaults.Toolset` plus a small Maestro search extension pack.
3. Wire sessionevent SQLite into the native runtime.
4. Add explicit create-or-get session bootstrap.
5. Expose run + session control methods.
6. Wire the typed `native.Config.EventSink` into a Maestro event adapter for TUI/CLI consumption.
7. Add `bash` environment passthrough configuration for repo-local developer workflows where needed.
8. Define the per-request agent-construction model for concurrency safety.
9. Add `Runtime.Close()` and wire shutdown ownership.
10. Provide an adapter path to the existing `MaestroBackend` contract.

#### Acceptance Criteria

- Maestro can execute a task through the native runtime against a repo workspace.
- Session and branch state persist across runs.
- The runtime is reusable by both CLI and TUI.
- Native lifecycle events are available to higher layers without bespoke polling.
- `/ask`-style search quality does not regress materially due to loss of dedicated search tools.
- Maestro can supply additional safe environment passthrough keys for `bash` when required by the workspace.
- the runtime can be shut down cleanly via `Runtime.Close()`
- the existing TUI backend contract can use the runtime without a mandatory UI-wide interface rewrite

---

### Phase 3: Move `/ask` to Native

#### Target Files

- `internal/orchestration/pool.go`
- `internal/orchestration/service.go`
- `internal/orchestration/processors.go`

#### Changes

1. Replace the current ReAct-backed QA path with the new native runtime.
2. Stop creating a fresh ReAct agent on every `Ask()`.
3. Keep the public `ProcessRequest` shape stable if possible.
4. Document the user-visible behavior change: `/ask` becomes stateful across requests.
5. Surface native event streaming for `/ask` execution immediately, not only in later TUI work.
6. Preserve Maestro's current search-heavy behavior via the search extension pack.
7. Route both natural-language questions and `/ask ...` through the same migrated backend path.

#### Acceptance Criteria

- `/ask` uses the native runtime, not `createReActAgent(...)`.
- repeated `/ask` requests preserve recall-based session continuity
- branch switching affects subsequent `/ask` responses
- previous `/ask` observations and answers can appear in session recall during later `/ask` runs
- natural-language questions in the TUI and explicit `/ask` commands both use the same migrated backend method

Important wording:

- this is recall-based continuity, not full cross-request conversation replay
- within one `Execute()` call, the native runtime preserves real turn history
- across `Execute()` calls, continuity comes from session recall and summaries

---

### Phase 4: Session Controls in TUI/CLI

#### Target Files

- `main.go`
- `terminal/*`
- any command handlers that expose slash commands

#### Changes

1. Rewire the existing session commands to native/sessionevent-backed storage.
2. Add branch switch/fork commands where missing.
3. Surface current session + branch in the status UI.
4. Stream typed native lifecycle events through the TUI using `native.Config.EventSink` and existing `ProgressMsg` plumbing.

#### Acceptance Criteria

- user can inspect session state from Maestro
- user can fork and switch branches without touching raw DB/state files
- current branch is visible during interactive use
- tool-call and run lifecycle events can drive visible progress in the UI
- existing session commands continue to work, now backed by the native session model

---

## Runtime Configuration

The native wrapper should expose a small config surface instead of hard-coding native session recall defaults.

Suggested knobs:

- `SessionRecallLimit`
- `SessionRecallMaxChars`
- `CommandTimeout`
- `ModelOutputLimit`
- `DisplayOutputLimit`
- `BashPassthroughEnvKeys`

Reason:

- native defaults are intentionally conservative
- Maestro interactive sessions are often longer-lived than example flows
- TUI/CLI layers should be able to tune recall without editing runtime internals
- some developer workflows need extra environment variables beyond the default filtered `bash` environment

---

### Phase 5: Deprecate File-Based Interactive Sessions

#### Target Files

- `internal/subagent/session.go`
- `internal/subagent/claude.go`
- `internal/subagent/gemini.go`

#### Changes

1. Narrow `context.md` session handling to legacy compatibility only.
2. Stop using it as the main interactive state mechanism.
3. Keep the file-based session directory only if legacy Claude/Gemini processors still require it as a subprocess I/O channel.
4. Optionally bridge Claude/Gemini file processors onto sessionevent-backed context later.

#### Acceptance Criteria

- native sessions are the canonical source of interactive state
- markdown session files are no longer required for normal Maestro runtime behavior

---

## What Stays Unchanged Initially

These parts should remain intact during the first migration:

- `internal/review/*`
- `internal/rlm/review_wrapper.go`
- PR review routing in `internal/orchestration/service.go`
- specialized review logic and comment processing

Reason:
- this stack is specialized and already product-specific
- replacing it early would create churn without the highest payoff

---

## Concrete Code Hotspots

These are the current update points that matter most:

1. `internal/orchestration/service.go`
   - fake SQLite memory path
   - current subagent session initialization
   - Maestro ACE manager initialization

2. `internal/orchestration/pool.go`
   - `/ask` currently creates a fresh ReAct agent per request
   - `/ask` is search-tool driven today

3. `internal/subagent/session.go`
   - file-based legacy subprocess session channel

4. `internal/agent/react.go`
   - current specialized interactive ReAct backbone

These should be treated as migration seams, not all rewritten at once.

---

## Risks

### 1. Over-migrating the Review Path

Risk:
- turning the specialized review engine into a generic agent migration project

Mitigation:
- keep review and interactive migration separate

### 2. Session Model Split-Brain

Risk:
- Maestro temporarily having both markdown sessions and sessionevent sessions

Mitigation:
- declare sessionevent canonical for interactive runtime as soon as phase 2 lands
- explicitly label markdown sessions as compatibility-only

### 3. Tool Surface Drift

Risk:
- Maestro reintroducing too many built-in tools after adopting a minimal pack

Mitigation:
- keep the default pack small
- add optional packs later, not by default

### 4. TUI Integration Churn

Risk:
- mixing UI modernization with runtime migration

Mitigation:
- get the runtime wrapper working before changing terminal UX deeply

---

## Decisions and Open Questions

### Decision: `/ask` becomes the general interactive entry point

Rationale:

- splitting `/ask` and `/agent` would create unnecessary user-facing fragmentation
- the native runtime can already handle both QA and coding tasks
- Maestro should keep one obvious interactive command and let tools determine behavior

### Open Questions

1. Should Claude/Gemini file processors be kept as legacy integrations, or eventually rewritten as native-agent-backed providers/tools?
2. Should branch selection change the active branch globally by default, or support a temporary branch-targeted read mode?

---

## Recommended First Implementation Slice

The first implementation slice should be intentionally small:

1. add a real `sessionevent` SQLite store to Maestro state
2. add `internal/native` runtime wrapper
3. add create-or-get session bootstrap
4. switch `/ask` from fresh-ReAct-per-request to the native runtime

This is the smallest change that makes Maestro materially more modern:

- real persistence
- real session continuity
- branchable sessions
- minimal default tools
- native runtime alignment with current `dspy-go`

---

## Review Checklist

Other agents reviewing this spec should explicitly check:

1. whether the review lane vs. interactive lane split is the right boundary
2. whether `/ask` should be the first migrated command
3. whether `internal/native` is the right package boundary
4. whether markdown session compatibility should be shorter- or longer-lived
5. whether Maestro should expose branch controls immediately or after `/ask` migration
