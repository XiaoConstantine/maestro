# Maestro RLM: Universal Context-Efficient Orchestration Layer

## Executive Summary

Maestro RLM aims to become a universal orchestration layer that makes AI coding assistants (Claude Code, Codex, and others) significantly more context-efficient. By implementing the Recursive Language Model (RLM) paradigm, users can work with large codebases in extended sessions without worrying about context window limitations or conversation compaction.

---

## The Problem

### Context Inefficiency in Current AI Coding Assistants

When developers use AI coding assistants like Claude Code or Codex directly, they face fundamental context inefficiencies:

**1. Context Window Bloat**
```
Session Start:
  User: "Explain the authentication system"
  Assistant: *reads auth.go, middleware.go, handlers.go*
  Context: ~15,000 tokens

30 minutes later:
  User: "Now explain the database layer"
  Assistant: *reads db.go, models.go, migrations.go*
  Context: ~45,000 tokens

1 hour later:
  User: "How do auth and db interact?"
  Assistant: *context approaching limits*
  Context: ~100,000+ tokens → COMPACTION TRIGGERED
```

**2. Conversation Compaction Loses Detail**
- When context limits are reached, assistants summarize/compact history
- Critical details from earlier analysis are lost
- User must re-explain or re-read files
- Session continuity breaks down

**3. Repeated Context Loading**
- Each question re-sends large code blocks
- Same files read multiple times across questions
- Token usage scales linearly with session length
- Cost accumulates unnecessarily

**4. No Cross-Assistant Portability**
- Context built up in Claude Code doesn't transfer to Codex
- Switching assistants means starting over
- No unified way to leverage multiple assistants' strengths

### Quantified Impact (Targets)

| Metric | Direct Approach | With RLM (Target) |
|--------|-----------------|-------------------|
| Tokens per large-context query | 25,000+ | ~2,500 |
| Session longevity before compaction | 30-60 min | 4+ hours (target) |
| Cost for 10 queries on 100KB codebase | ~$7.50 | ~$0.75 |
| Context retained after 1 hour | ~40% | ~95%+ (target) |

*Targets assume a 100KB codebase and comparable prompt complexity; validate with benchmarks.*

---

## The Vision

### Maestro as the Context-Efficient Layer

Maestro RLM sits between the user and their AI coding assistant(s), providing:

```
┌─────────────────────────────────────────────────────────────┐
│                     User's Workflow                         │
│                                                             │
│  "I want to understand this 500KB codebase and ask          │
│   questions for hours without losing context"               │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                   Maestro RLM Layer                         │
│                                                             │
│  • State persisted in REPL variables (not conversation)     │
│  • Targeted slice queries (not full context dumps)          │
│  • Cross-session continuity (checkpoints & resume)          │
│  • Multi-agent orchestration (use the right tool)           │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                   Sub-Agent Adapters                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ Claude Code  │  │    Codex     │  │   Future     │      │
│  │   Adapter    │  │   Adapter    │  │  Adapters    │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### Key Principles

1. **State Lives Outside Context**
   - Code analysis results stored in REPL variables
   - File summaries, findings, patterns cached
   - Conversation stays lightweight

2. **Targeted Queries, Not Context Dumps**
   - RLM identifies relevant slices
   - Sub-agents receive only what they need
   - 90%+ reduction in tokens per query

3. **Assistant Agnostic**
   - Same interface works with Claude Code, Codex, others
   - User chooses preferred backend(s)
   - Apples-to-apples efficiency comparison

4. **Session Persistence**
   - Checkpoint state for long-running analysis
   - Resume without re-reading files
   - Cross-session continuity

---

## Proposed Architecture

### Core Components

```
┌─────────────────────────────────────────────────────────────┐
│                    RLM Orchestrator                         │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                   REPL Engine                        │   │
│  │  • Variable storage (findings, summaries, etc.)     │   │
│  │  • Built-in functions (Query, FindRelevant, etc.)   │   │
│  │  • Iteration control and checkpointing              │   │
│  └─────────────────────────────────────────────────────┘   │
│                            │                                │
│                            ▼                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │               Context Index                          │   │
│  │  • Chunked code with embeddings                     │   │
│  │  • Semantic search (FindRelevant)                   │   │
│  │  • File/function/class summaries                    │   │
│  └─────────────────────────────────────────────────────┘   │
│                            │                                │
│                            ▼                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              Sub-Agent Router                        │   │
│  │  • Routes queries to appropriate agent              │   │
│  │  • Tracks token usage per agent                     │   │
│  │  • Manages rate limits and fallbacks                │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            │
            ┌───────────────┼───────────────┐
            ▼               ▼               ▼
     ┌────────────┐  ┌────────────┐  ┌────────────┐
     │Claude Code │  │   Codex    │  │   Others   │
     │  Adapter   │  │  Adapter   │  │            │
     └────────────┘  └────────────┘  └────────────┘
```

### Sub-Agent Interface

```go
// SubAgent abstracts different AI coding assistants
type SubAgent interface {
    // Query sends a targeted prompt and returns response with metrics
    Query(ctx context.Context, req QueryRequest) (*QueryResponse, error)

    // Name returns the agent identifier
    Name() string

    // Capabilities returns what this agent can do
    Capabilities() []Capability

    // TokenPricing returns cost per 1K tokens (input, output)
    TokenPricing() (input float64, output float64)
}

type QueryRequest struct {
    Prompt      string
    MaxTokens   int
    Temperature float64
    Context     map[string]any  // Optional context hints
}

type QueryResponse struct {
    Response     string
    InputTokens  int
    OutputTokens int
    Duration     time.Duration
    Metadata     map[string]any
}

type Capability int
const (
    CapabilityCodeAnalysis Capability = iota
    CapabilityCodeGeneration
    CapabilityFileRead
    CapabilityFileWrite
    CapabilityWebSearch
    CapabilityShellExecution
)
```

### Adapter Implementations (Illustrative)

**Claude Code Adapter:**
```go
type ClaudeCodeAdapter struct {
    cliPath     string      // Path to claude CLI
    sessionID   string      // For session continuity
    tokenTracker *TokenTracker
}

func (a *ClaudeCodeAdapter) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
    // Option 1: CLI invocation
    cmd := exec.CommandContext(ctx, a.cliPath, "--print", req.Prompt)

    // Option 2: SDK/API (if available)
    // client.SendMessage(req.Prompt)

    // Track tokens from response metadata
    // Return structured response
}
```

**OpenAI Adapter (using dspy-go):**
```go
type OpenAIAdapter struct {
    llm      core.LLM  // dspy-go OpenAILLM
    modelID  core.ModelID
}

func NewOpenAIAdapter(modelID string, apiKey string) (*OpenAIAdapter, error) {
    // dspy-go handles OpenAI model selection
    llm, err := llms.NewOpenAI(core.ModelID(modelID), apiKey)
    if err != nil {
        return nil, err
    }
    return &OpenAIAdapter{llm: llm, modelID: core.ModelID(modelID)}, nil
}

func (a *OpenAIAdapter) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
    resp, err := a.llm.Generate(ctx, req.Prompt)
    if err != nil {
        return nil, err
    }
    return &QueryResponse{
        Response:     resp.Content,
        InputTokens:  resp.Usage.PromptTokens,
        OutputTokens: resp.Usage.CompletionTokens,
    }, nil
}

// Supported models depend on the OpenAI API and dspy-go version.
```

---

## Implementation Plan

### Phase 1: Foundation (Current State)
- [x] RLM processor with dspy-go integration
- [x] Tiered sub-client with token tracking
- [x] Basic /rlm command in Maestro TUI
- [x] Token efficiency metrics and display

### Phase 2: Sub-Agent Abstraction
- [ ] Define SubAgent interface
- [ ] Refactor TieredSubClient to implement SubAgent
- [ ] Add adapter registry for multiple backends
- [ ] Token tracking per adapter

### Phase 3: Claude Code Adapter
- [ ] Research Claude Code CLI/SDK integration options
- [ ] Implement ClaudeCodeAdapter
- [ ] Add session management for continuity
- [ ] Baseline comparison mode (direct vs RLM)

### Phase 4: OpenAI/Codex Integration
- [ ] Wire OpenAI provider into Maestro's model config (`--provider openai`)
- [ ] Support model selection: `gpt-4o`, `o3`, `gpt-5.2-codex`, etc.
- [ ] Add OpenAI OAuth support (ChatGPT Plus/Pro subscriptions)
- [ ] Environment variables: `OPENAI_API_KEY`, `OPENAI_OAUTH_TOKEN`

**Note:** dspy-go already supports OpenAI models including:
- GPT-4 series: `gpt-4`, `gpt-4-turbo`, `gpt-4o`, `gpt-4o-mini`
- GPT-4.1 series: `gpt-4.1`, `gpt-4.1-mini`, `gpt-4.1-nano`
- Reasoning models: `o1`, `o1-pro`, `o1-mini`, `o3`, `o3-mini`
- GPT-5 series: `gpt-5`, `gpt-5-mini`, `gpt-5-nano`
- GPT-5.2 series: `gpt-5.2`, `gpt-5.2-instant`, `gpt-5.2-thinking`, `gpt-5.2-codex`

### Phase 5: Comparison & Benchmarking
- [x] A/B test mode: run both direct and RLM
- [x] Benchmark suite for different codebase sizes
- [x] Generate efficiency reports
- [ ] Publish comparison data

### Phase 6: Advanced Features
- [x] Cross-agent orchestration (use Claude for analysis, Codex for generation)
- [x] Checkpoint/resume for long sessions
- [x] Context index persistence across sessions
- [x] Real-time token budget management

---

## Success Metrics

### Efficiency Metrics (Targets)
| Metric | Target |
|--------|--------|
| Token reduction vs direct | >80% |
| Session longevity | >4 hours without compaction |
| Query latency overhead | <20% |
| Cost reduction | >70% |

### User Experience Metrics (Targets)
| Metric | Target |
|--------|--------|
| Answer quality parity | >95% equivalent (target) |
| Setup complexity | <5 min |
| Learning curve | Single command (/rlm) |

### Adoption Metrics (Targets)
| Metric | Target |
|--------|--------|
| Supported assistants | 3+ (Claude Code, OpenAI, one other) |
| Active users | Track via opt-in telemetry |

---

## Open Questions

1. **Claude Code Integration**
   - Is there a programmatic SDK, or CLI-only?
   - How to track token usage from CLI output?
   - Session management across invocations?

2. **OpenAI Integration** *(Partially Resolved)*
   - dspy-go supports OpenAI models; confirm supported model IDs and pricing
   - Wire `--provider openai` into Maestro's CLI
   - Confirm auth mechanism (API key vs OAuth) and env var names

3. **Baseline Measurement**
   - Should we actually run direct queries for comparison (2x cost)?
   - Or use accurate token counting without execution?
   - Could use tiktoken for accurate OpenAI token counts

4. **State Persistence**
   - Where to store REPL state between sessions?
   - How to handle stale state when codebase changes?

5. **Multi-Agent Routing**
   - How to decide which agent handles which query?
   - User preference vs automatic selection?
   - Cost-based routing (use cheaper models for simple queries)?

---

## Appendix A: dspy-go Provider Support

dspy-go already provides LLM abstractions for multiple providers:

| Provider | Models | Environment Variables |
|----------|--------|----------------------|
| **Anthropic** | Claude 3.5 Sonnet, Claude 3 Opus, etc. | `ANTHROPIC_API_KEY`, `ANTHROPIC_OAUTH_TOKEN` |
| **OpenAI** | GPT-4o, o3, GPT-5.2-Codex, etc. | `OPENAI_API_KEY`, `OPENAI_OAUTH_TOKEN` |
| **Google** | Gemini Pro, Gemini Flash | `GOOGLE_API_KEY`, `GEMINI_API_KEY` |
| **Ollama** | Local models (Llama, Mistral, etc.) | N/A (local) |
| **LlamaCpp** | Local GGUF models | N/A (local) |

**OpenAI Model Tiers for RLM:**
```
TierFast  → gpt-4o-mini, gpt-5.2-instant  (cheap, high-volume)
TierSmart → gpt-4o, o3-mini               (balanced)
TierBest  → o3, gpt-5.2-codex             (frontier, synthesis)
```

**Anthropic Model Tiers for RLM:**
```
TierFast  → claude-3-haiku     (cheap, high-volume)
TierSmart → claude-3.5-sonnet  (balanced)
TierBest  → claude-3-opus      (frontier, synthesis)
```

---

## Appendix B: RLM Paradigm Overview

The Recursive Language Model paradigm (inspired by MIT OASYS Lab research) addresses context limitations by:

1. **Variables over Verbalization**: Store intermediate results in REPL variables, not conversation text
2. **Code over Prose**: LLM writes code to explore context, not prose descriptions
3. **Targeted Queries**: Sub-agents receive minimal slices, not full context

```
Traditional Approach:
  User → [100KB context + query] → LLM → Response
  Tokens: 25,000+

RLM Approach:
  User → Query
  RLM Orchestrator:
    1. FindRelevant("error handling") → 3 relevant chunks
    2. Query(chunk1, "analyze") → finding1
    3. Query(chunk2, "analyze") → finding2
    4. Query(chunk3, "analyze") → finding3
    5. Synthesize(findings) → Response
  Tokens: ~2,500 (90% reduction)
```

---

*Document Version: 1.0*
*Last Updated: 2026-01-25*
*Authors: Maestro Team*
