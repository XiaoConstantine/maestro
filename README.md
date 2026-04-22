# Maestro - Advanced AI-Powered Code Review Assistant

Maestro is an AI code review and repository analysis assistant built on top of `dspy-go`. It is no longer just a one-shot PR review CLI: the repo now includes a live review path, a repository ask path with RLM-backed long-context support, benchmark-driven optimization commands, and a staged self-evolution loop for reviewer improvement.

## 🏗️ Architecture Overview

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│                               USER INTERFACE                                │
│                                                                              │
│  CLI / TUI                                                                  │
│  • PR review                                                                │
│  • /ask repository questions                                                │
│  • optional review artifact + skill-store loading                           │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              MAESTRO SERVICE                                │
│                                                                              │
│  Root CLI + TUI routing                                                     │
│  • review path                                                              │
│  • ask / overview path                                                      │
│  • model and provider wiring                                                │
│  • ACE + persisted skill integration                                        │
└───────────────┬───────────────────────────────────────┬──────────────────────┘
                │                                       │
                ▼                                       ▼
┌──────────────────────────────┐         ┌─────────────────────────────────────┐
│         REVIEW ENGINE         │         │           ASK / OVERVIEW            │
│                               │         │                                     │
│  • PRReviewAgent              │         │  • repo ask orchestration           │
│  • chunked review pipeline    │         │  • RLM-backed overview path         │
│  • parallel review workers    │         │  • adaptive replay / sub-RLM caps   │
│  • guideline lookup           │         │                                     │
└───────────────┬───────────────┘         └──────────────────┬──────────────────┘
                │                                            │
                └──────────────────────┬─────────────────────┘
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                         PERSISTED RUNTIME STATE                              │
│                                                                              │
│  • review artifacts / optimized_program.json                                │
│  • review skill store                                                       │
│  • ACE learnings                                                            │
│  • local Maestro state directories                                          │
└──────────────────────────────────────────────────────────────────────────────┘


┌──────────────────────────────────────────────────────────────────────────────┐
│                         OFFLINE LEARNING LANE                               │
│                                                                              │
│  optimize-review / optimize-qa                                              │
│  • GEPA benchmark runs                                                      │
│  • persisted optimized-program artifacts                                    │
│                                                                              │
│  evolve-review                                                              │
│  • search suite -> GEPA search                                              │
│  • full replay suite -> promotion check                                     │
│  • protected suite -> generalization gate                                   │
│  • publish current reviewer on success                                      │
└──────────────────────────────────────────────────────────────────────────────┘
```

## 🎯 Core Features

### **Advanced Context Analysis**
- **Chunked Review Pipeline**: Maestro reviews changed code in chunked passes with line-grounded findings.
- **Guideline Integration**: review runs can use cached guidelines and repository-aware context.
- **Artifact-Aware Runtime**: live review runs can load tuned review artifacts and persisted skill stores.
- **Repository Ask Support**: Maestro can answer codebase questions in addition to reviewing PRs.

### **Intelligent Review Pipeline**
- **Specialized PR Review Agent**: Maestro uses a dedicated Go review path rather than a generic chat wrapper.
- **Parallel Review Execution**: chunk evaluation runs concurrently for throughput on larger PRs.
- **Review Filtering and Verification**: the review path includes post-processing to suppress weak or off-target comments.
- **Benchmark-Driven Optimization**: the review benchmark and evaluator can drive GEPA tuning offline.

### **GitHub Integration**
- **Direct PR Review**: review GitHub pull requests from the CLI or TUI.
- **Existing Comment Awareness**: Maestro processes prior PR comment context during review runs.
- **Token Verification**: the root CLI verifies GitHub permissions before operating.

### **Terminal UI (TUI v2)**
- **Interactive Mode**: the root command still launches the modern interactive interface when no PR is provided.
- **Slash-Command Workflow**: Maestro supports review and ask-style interaction patterns.
- **Shared Runtime Wiring**: the same service layer backs both direct CLI review and interactive usage.

### **Semantic Code Search (Sgrep)**
- **Search Tooling**: Maestro has an sgrep-backed search path and test coverage around its runtime environment wiring.
- **Guideline / Context Lookup**: the review engine can incorporate indexed repository guidance.
- **RLM Companion Role**: semantic lookup complements the long-context overview lane.

### **Unified Agent Architecture**
- **Single Root CLI**: [`main.go`](./main.go) is still the normal entry point.
- **Live Review + Ask + Offline Tuning**: Maestro now spans serving and offline learning in one repo.
- **Persisted Reviewer Consumption**: the live review path can load a reviewer produced by `optimize-review` or `evolve-review`.

### **Flexible Model Support**
- **`dspy-go` Model Abstraction**: Maestro uses the same provider/model abstraction layer as `dspy-go`.
- **Primary + Teacher Model Support**: optimization commands support separate student and teacher models.
- **Deterministic Eval Support**: the evolution lane can force evaluation temperature to `0` for stability.

## 🛠️ Enhanced Technical Capabilities

### **Review Dimensions**
- **Correctness-first Review**: Maestro is currently tuned toward concrete, actionable Go findings.
- **Behavior / API Regression Awareness**: the current review seed explicitly includes API contract and behavior regressions.
- **Negative-case Suppression**: the benchmark/evaluator penalizes low-value or noisy comments.

### **Advanced Features**
- **Persisted Optimized Programs**: review and QA optimization now use the `dspy-go.optimized-agent-program` envelope.
- **Forward-Compatible Restore**: obsolete target IDs in saved optimized programs are skipped on restore.
- **Staged Self-Evolution**: `evolve-review` separates cheaper GEPA search from full replay validation.
- **Promotion Gates**: main replay regression tolerance and protected-suite regression tolerance are both supported.
- **Retention + Circuit Breaker**: the evolution runner can prune historical runs and stop after repeated failures.

## 📦 Getting Started

### **Prerequisites**
- Go 1.24+
- GitHub token with PR access
- a supported model backend configured through `dspy-go`

### **Installation**

```bash
git clone https://github.com/XiaoConstantine/maestro.git
cd maestro
go mod download
go build ./...
```

### **Local Model Setup (Optional)**

Maestro still supports local or custom model endpoints through the shared `dspy-go` provider layer. For example, local OpenAI-compatible endpoints can be used through `--model`, `--provider`, and `--base-url`.

### **Quick Start**

```bash
# Launch interactive mode
go run .

# Review a PR directly
go run . \
  --owner XiaoConstantine \
  --repo dspy-go \
  --pr 291 \
  --model google:gemini-2.5-flash

# Review with a tuned reviewer
go run . \
  --owner XiaoConstantine \
  --repo dspy-go \
  --pr 291 \
  --model google:gemini-2.5-flash \
  --review-artifacts ~/.maestro/evolution/review/rsc/current/optimized_program.json \
  --review-skill-store ~/.maestro/evolution/review/rsc/skills.json
```

## ⚙️ Configuration

### **Environment Variables**

#### **Core Configuration**
```bash
MAESTRO_GITHUB_TOKEN=your_token
ANTHROPIC_API_KEY=your_key
GOOGLE_API_KEY=your_key
MAESTRO_REVIEW_ARTIFACTS=/path/to/optimized_program.json
MAESTRO_REVIEW_SKILL_STORE=/path/to/skills.json
MAESTRO_REVIEW_SKILL_DOMAIN=maestro:review:go
MAESTRO_RLM_OVERVIEW_SKILL_STORE=/path/to/rlm_skills.json
```

#### **Enhanced Processing**
```bash
MAESTRO_LOG_LEVEL=debug
MAESTRO_RAG_DEBUG_ENABLED=true
MAESTRO_REVIEW_ARTIFACTS=/path/to/review_optimized_program.json
```

#### **Sgrep / Local Embeddings**
Maestro still supports local search/indexing flows, and the repo includes sgrep-related tests and review engine integration. Exact provider wiring depends on your local environment and `dspy-go` model configuration.

#### **Feature Toggles**
The repo still contains ACE and RLM-related runtime wiring, but the most important operational knobs now live in the review/evolution commands themselves rather than only in environment flags.

### **Command Line Options**
- Root CLI:
  - `--owner`
  - `--repo`
  - `--pr`
  - `--model`
  - `--github-token`
  - `--review-artifacts`
  - `--review-skill-store`
  - `--review-skill-domain`

- `cmd/optimize-review`:
  - `--suite`
  - `--artifact`
  - `--teacher-model`
  - `--population`
  - `--generations`
  - `--validation-frequency`
  - `--max-metric-calls`
  - `--max-runtime`

- `cmd/evolve-review`:
  - `--state-dir`
  - `--suite`
  - `--search-suite`
  - `--protected-suite`
  - `--regression-tolerance`
  - `--protected-regression-tolerance`
  - `--max-runtime`

### **Model Selection**

```bash
# Gemini
go run . --model google:gemini-2.5-flash --owner XiaoConstantine --repo dspy-go --pr 291

# OpenAI-compatible local endpoint
go run . --model openai:Qwen3.5-9B-OptiQ-4bit --base-url http://127.0.0.1:8081 --owner XiaoConstantine --repo dspy-go --pr 291

# Optimize review with a separate teacher model
go run ./cmd/optimize-review \
  --suite ~/.maestro/review/corpora/rsc-golang-org/review_go_suite.json \
  --model google:gemini-2.5-flash \
  --teacher-model google:gemini-2.5-pro
```

## 📊 Performance & Metrics

### **Current Scale**
- **Live Review**: operational on real PRs through the root CLI.
- **Offline Optimization**: review and QA optimization commands now persist optimized programs.
- **Evolution Runner**: the staged `evolve-review` loop can now complete end to end and publish a reviewer.

### **Recent Improvements**
- **Optimized-program restore**: Maestro consumes the newer `dspy-go` optimized-program envelope.
- **Structured RLM replay alignment**: the RLM overview lane is aligned with the newer adaptive replay / sub-RLM controls.
- **Deterministic evaluation**: evolution runs can set evaluation temperature to `0`.
- **Staged search**: GEPA breadth is no longer forced to collapse just to keep full replay affordable.
- **Protected gating**: protected-suite replay is deferred and gated separately from the main lane.

## 🔬 Advanced Usage

### **Debug Mode**
```bash
go run . \
  --owner XiaoConstantine \
  --repo dspy-go \
  --pr 291 \
  --model google:gemini-2.5-flash \
  --verbose
```

### **Performance Tuning**
```bash
# One-off benchmark optimization
go run ./cmd/optimize-review \
  --suite ~/.maestro/review/corpora/rsc-golang-org/review_go_suite.json \
  --population 4 \
  --generations 2 \
  --max-metric-calls 20

# Staged self-evolution
go run ./cmd/evolve-review \
  --state-dir ~/.maestro/evolution/review/rsc \
  --search-suite ~/.maestro/review/corpora/rsc-golang-org/review_go_train_70_30.json \
  --suite ~/.maestro/review/corpora/rsc-golang-org/review_go_suite.json \
  --protected-suite ~/.maestro/review/corpora/mdempsky-google-com/review_go_suite.json \
  --eval-temperature 0 \
  --regression-tolerance 0.015 \
  --protected-regression-tolerance 0.04 \
  --population 8 \
  --generations 4
```

### **Current Caveats**
- The self-evolution control plane works.
- Review-quality gains are still corpus-sensitive.
- The first successful promotion proved the pipeline, not final reviewer quality.
- Benchmark wins and live PR-review wins are related, but not interchangeable.

## 📄 License

Maestro is released under the MIT License. See the [LICENSE](LICENSE) file for details.
