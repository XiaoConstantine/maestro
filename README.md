# Maestro - Advanced AI-Powered Code Review Assistant

Maestro is an intelligent code review assistant built with DSPy-Go that provides comprehensive, file-level code analysis for GitHub pull requests. It combines advanced AST parsing, semantic analysis, and LLM-powered reasoning to deliver high-quality, actionable code review feedback.


## 🏗️ Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              USER INTERFACE                                     │
│  ┌─────────────────┐  ┌─────────────────────────────┐  ┌─────────────────────┐  │
│  │    CLI Mode     │  │   TUI v2 (Bubbletea)        │  │    GitHub API       │  │
│  │                 │  │  ┌─────────────────────────┐│  │                     │  │
│  │  • Spinner      │  │  │ Vim Keybindings        ││  │  • PR Changes       │  │
│  │  • Progress     │  │  │ Command Palette        ││  │  • Review Comments  │  │
│  │  • Prompts      │  │  │ File Tree / TODOs      ││  │  • OAuth2           │  │
│  │                 │  │  │ Review Display         ││  │                     │  │
│  └────────┬────────┘  │  └─────────────────────────┘│  └──────────┬──────────┘  │
│           │           └──────────────┬──────────────┘             │             │
└───────────┼──────────────────────────┼────────────────────────────┼─────────────┘
            │                          │                            │
            ▼                          ▼                            ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           MAESTRO SERVICE                                       │
│               (Singleton - created once per session)                            │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │                         AgentPool (Persistent)                          │   │
│  │  ┌─────────────────────┐              ┌─────────────────────────────┐   │   │
│  │  │   PRReviewAgent     │              │   UnifiedReActAgent (QA)    │   │   │
│  │  │   (persistent)      │              │   (persistent, shared mem)  │   │   │
│  │  └─────────────────────┘              └─────────────────────────────┘   │   │
│  │                                                                          │   │
│  │  ┌─────────────────────┐              ┌─────────────────────────────┐   │   │
│  │  │   ClaudeProcessor   │              │   GeminiProcessor           │   │   │
│  │  │   (Sonnet 4.5)      │              │   (Gemini 3 Pro Preview)    │   │   │
│  │  └─────────────────────┘              └─────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                           │
│  ┌──────────────────────────────────┴───────────────────────────────────────┐  │
│  │                      ProcessRequest Router                                │  │
│  │   /review → ReviewAgent    /ask → QAAgent    /claude    /gemini          │  │
│  └──────────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
          ┌───────────────────────────┼───────────────────────────┐
          ▼                           ▼                           ▼
┌─────────────────────┐    ┌──────────────────────┐    ┌─────────────────────┐
│    REVIEW ENGINE    │    │   UNIFIED REACT      │    │   SEARCH ENGINE     │
│                     │    │      AGENT           │    │                     │
│ • Chunker           │    │ • Iterative Search   │    │ • Semantic Search   │
│ • Workflow Chain    │    │ • Tool Selection     │    │ • Hybrid Matching   │
│ • Parallel Review   │    │ • Context Management │    │ • Code Indexing     │
│ • 120 Workers       │    │ • Quality Tracking   │    │                     │
└─────────────────────┘    └──────────────────────┘    └─────────────────────┘
          │                           │                           │
          └───────────────────────────┼───────────────────────────┘
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          INTELLIGENCE LAYER                                     │
│  ┌────────────────────┐  ┌───────────────────┐  ┌────────────────────────────┐  │
│  │   Sgrep Search     │  │     RAG Store     │  │   LLM Orchestration        │  │
│  │                    │  │                   │  │       (DSPy-Go)            │  │
│  │ • Semantic Search  │  │ • Guidelines      │  │                            │  │
│  │ • Hybrid Search    │  │ • Code Patterns   │  │  • Anthropic / Gemini      │  │
│  │ • Code Indexing    │  │ • Similarity      │  │  • Ollama / LLaMA.cpp      │  │
│  └─────────┬──────────┘  └─────────┬─────────┘  └─────────────┬──────────────┘  │
└────────────┼───────────────────────┼──────────────────────────┼─────────────────┘
             │                       │                          │
             ▼                       ▼                          ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                             DATA LAYER                                          │
│  ┌────────────────────────┐  ┌─────────────────────┐  ┌──────────────────────┐  │
│  │   SQLite + sqlite-vec  │  │  Embedding Router   │  │   Shared Memory      │  │
│  │                        │  │                     │  │   (Configurable)     │  │
│  │  • Vector Storage      │  │  • Local (Sgrep)    │  │  • InMemory          │  │
│  │  • Metadata Index      │  │  • Cloud Fallback   │  │  • SQLite (optional) │  │
│  │  • Pattern Matching    │  │  • Smart Routing    │  │                      │  │
│  └────────────────────────┘  └─────────────────────┘  └──────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## 🎯 Core Features

### **Advanced Context Analysis**
- **AST-Based Parsing**: Deep Go code structure analysis including packages, imports, types, and functions
- **File-Level Context**: Comprehensive understanding of code relationships and dependencies  
- **Semantic Purpose Detection**: Automatic identification of code chunk functionality
- **Enhanced Chunk Metadata**: Rich context with 15+ lines of surrounding code

### **Intelligent Review Pipeline**
- **Multi-Stage Processing**: Context preparation → Analysis → Validation → Aggregation
- **File-Level Aggregation**: Groups related issues by file with intelligent deduplication
- **Advanced Debugging**: Comprehensive logging and performance metrics
- **Configurable Processing**: Environment variables for fine-tuning behavior

### **GitHub Integration**
- **Seamless PR Workflow**: Direct integration with GitHub pull request comments
- **Bulk Processing**: Efficient handling of large PRs with parallel processing
- **Real-time Feedback**: Live processing status and progress indicators

### **Terminal UI (TUI v2)**
- **Modern Framework**: Built on Bubbletea v2 + Lipgloss v2 with real-time updates
- **Vim-Style Navigation**: Full Vim keybindings with Normal, Insert, Visual, Command, and Search modes
- **Multi-Mode Interface**: Input mode, Review mode, and Dashboard mode
- **Fuzzy Command Palette**: Quick command access with autocomplete and history
- **PR Review Display**: Inline comments grouped by file with severity-based color coding
- **File Tree Explorer**: Navigate repository with expand/collapse and filtering
- **Task Tracking**: Built-in TODO list for agent planning visibility
- **Split-Pane Layout**: IDE-like interface with configurable panels

### **Semantic Code Search (Sgrep)**
- **Semantic Understanding**: Search code by meaning, not just keywords
- **Hybrid Search**: Combine semantic + keyword matching for precise results
- **Embedding Provider**: Local embeddings via nomic-embed-text model (768 dimensions)
- **Guideline Discovery**: Semantic search for code guidelines and patterns
- **Agent Integration**: Available as a tool for ReAct agents during reasoning

### **Unified Agent Architecture**
- **MaestroService**: Singleton service created once per session for unified request handling
- **Persistent Agents**: Agent pool maintains persistent agents across requests (no ad-hoc creation)
- **Shared Memory**: Configurable memory backend (InMemory or SQLite) shared across agents
- **Request Routing**: Single entry point (`ProcessRequest`) routes `/review`, `/ask`, `/claude`, and `/gemini` commands
- **Multi-Model Subagents**: Claude (Sonnet 4.5) and Gemini (3 Pro Preview) available as subagents with file-based context sharing

### **Flexible Model Support**
- **Multiple Backends**: Anthropic Claude, Google Gemini, Local models (Ollama, LLaMA.cpp)
- **Unified Embedding**: Consistent vector representations for code and guidelines
- **Performance Optimization**: Intelligent model selection and caching

## 🛠️ Enhanced Technical Capabilities

### **Review Dimensions**
- **Code Defects**: Logic flaws, error handling issues, resource management
- **Security Vulnerabilities**: Injection attacks, insecure data handling, authentication issues
- **Maintainability**: Code organization, documentation, naming conventions, complexity
- **Performance**: Algorithmic efficiency, data structures, resource utilization

### **Advanced Features**
- **Vector-Based Similarity**: SQLite with sqlite-vec for efficient code pattern matching
- **Deduplication Engine**: Levenshtein distance-based issue consolidation
- **Context Extraction**: Go AST parsing with semantic analysis
- **Background Indexing**: Non-blocking repository analysis
- **Parallel Processing**: Concurrent chunk analysis for performance

## 📦 Getting Started

### **Prerequisites**
- Go 1.24.2 or higher
- SQLite with sqlite-vec extension
- GitHub API access token
- Supported LLM backend (Claude, Gemini, or local model)
- For local models: llama.cpp installed (can be automated with `make setup`)

### **Installation**

```bash
# Clone the repository
git clone https://github.com/XiaoConstantine/maestro.git
cd maestro

# Install dependencies
go mod download

# Build the binary
go build -o maestro

# Set up GitHub token
export MAESTRO_GITHUB_TOKEN=your_github_token
```

### **Local Model Setup (Optional)**

For using local LLM models with llama.cpp:

```bash
# One-time setup: Install llama.cpp and download models
# Creates ~/.maestro/models and downloads:
# - nomic-embed-text-v1.5.Q8_0.gguf (embedding model)
# - Qwen3-1.7B-Q8_0.gguf (inference model)
make setup

# Start both llama servers in separate tmux sessions
# Embedding server runs on port 8080
# LLM server runs on port 8081
make local

# View server logs
tmux attach -t embedding  # Embedding server logs
tmux attach -t llm        # LLM server logs
```

### **Quick Start**

```bash
# Launch TUI (default when no PR specified)
./maestro

# Or explicitly with interactive flag
./maestro -i

# Direct CLI usage for a specific PR
./maestro --owner=username --repo=repository --pr=123

# In TUI mode, use slash commands:
maestro> /review 123          # Review PR #123
maestro> /ask how does auth work?  # Ask about the codebase

# With enhanced debugging
export MAESTRO_LOG_LEVEL=debug
export MAESTRO_RAG_DEBUG_ENABLED=true
./maestro --owner=username --repo=repository --pr=123 --verbose
```

## ⚙️ Configuration

### **Environment Variables**

#### **Core Configuration**
```bash
MAESTRO_GITHUB_TOKEN=your_token          # GitHub API access
MAESTRO_LOG_LEVEL=debug                  # Logging level (debug, info, warn, error)
ANTHROPIC_API_KEY=your_key               # For /claude subagent (Claude Sonnet 4.5)
GOOGLE_API_KEY=your_key                  # For /gemini subagent (Gemini 3 Pro Preview)
```

#### **Enhanced Processing (Phase 4.1)**
```bash
# File-level aggregation
MAESTRO_FILE_AGGREGATION_ENABLED=true    # Enable file-level result aggregation
MAESTRO_DEDUPLICATION_THRESHOLD=0.8      # Issue similarity threshold (0.0-1.0)

# AST Context extraction  
MAESTRO_CONTEXT_EXTRACTION_ENABLED=true  # Enable AST-based context extraction
MAESTRO_CHUNK_CONTEXT_LINES=15          # Lines of context around chunks
MAESTRO_ENABLE_SEMANTIC_CONTEXT=true     # Enable semantic purpose detection
MAESTRO_ENABLE_DEPENDENCY_ANALYSIS=true  # Enable dependency tracking

# Advanced debugging
MAESTRO_RAG_DEBUG_ENABLED=true           # RAG retrieval debugging
MAESTRO_LLM_RESPONSE_DEBUG=true          # LLM response debugging  
MAESTRO_SIMILARITY_LOGGING=true          # Similarity score logging
```

#### **Sgrep / Local Embeddings**
```bash
MAESTRO_LOCAL_EMBEDDING_ENABLED=true     # Enable local embeddings
MAESTRO_LOCAL_EMBEDDING_PROVIDER=sgrep   # Provider: sgrep, ollama, llamacpp
MAESTRO_LOCAL_EMBEDDING_MODEL=nomic-embed-text  # Embedding model (768 dims for sgrep)
```

#### **Feature Toggles**
```bash
MAESTRO_ENHANCED_REASONING=true          # Enhanced reasoning capabilities
MAESTRO_COMMENT_REFINEMENT=true          # Comment refinement processing
MAESTRO_CONSENSUS_VALIDATION=true        # Consensus validation
```

### **Command Line Options**
- `--github-token`: GitHub personal access token
- `--owner`: Repository owner
- `--repo`: Repository name
- `--pr`: Pull request number to review
- `--model`: LLM backend selection
- `--index-workers`: Concurrent indexing workers
- `--review-workers`: Concurrent review workers
- `--verbose`: Enable detailed logging
- `-i, --interactive`: Launch TUI (also default when no PR specified)

### **TUI Commands**
- `/help`: Show available commands
- `/review <pr>`: Review a pull request
- `/ask <question>`: Ask questions about the repository (uses ReAct agent)
- `/claude <prompt>`: Send prompt to Claude Sonnet 4.5 subagent
- `/gemini <prompt>`: Send prompt to Gemini 3 Pro Preview subagent
- `/clear`: Clear conversation history
- `/exit`, `/quit`: Exit the TUI

### **TUI Keybindings**

#### **Normal Mode**
| Key | Action |
|-----|--------|
| `h/j/k/l` or arrows | Navigate |
| `i` | Enter Insert mode |
| `v` | Enter Visual mode |
| `:` | Enter Command mode |
| `/` | Enter Search mode |
| `g` / `G` | Jump to top / bottom |
| `ctrl+u` / `ctrl+d` | Page up / down |
| `tab` | Cycle focus between panes |
| `ctrl+b` | Toggle file tree |
| `ctrl+t` | Toggle TODO panel |
| `enter` | Select / expand item |
| `space` | Expand/collapse in file tree |
| `x` | Mark TODO complete |
| `q` / `ctrl+c` | Quit |

#### **Review Mode**
| Key | Action |
|-----|--------|
| `j/k` | Navigate comments |
| `space` | Expand/collapse file groups |
| `enter` / `l` | Toggle detail view |
| `h` | Close detail view |
| `tab` | Switch to input |
| `esc` | Clear review results |

### **Model Selection**

```bash
# Use local LLaMA.cpp
./maestro --model="llamacpp:" --pr=123

# Use Ollama with specific model
./maestro --model="ollama:codellama" --pr=123

# Use Anthropic Claude
./maestro --model="anthropic:claude-3-sonnet" --api-key=your_key --pr=123

# Use Google Gemini
./maestro --model="google:gemini-pro" --api-key=your_key --pr=123
```

## 📊 Performance & Metrics

### **Current Scale**
- **Codebase**: 27,000+ lines across 17 internal packages
- **Dependencies**: DSPy-Go v0.66.0, SQLite-vec, GitHub API v68
- **Processing**: Handles 300+ chunks per PR with file-level aggregation
- **Performance**: ~500ms average per chunk with parallel processing

### **Recent Improvements**
- **Multi-Model Subagents**: `/claude` (Sonnet 4.5) and `/gemini` (Gemini 3 Pro Preview) commands with file-based context sharing
- **Unified Architecture**: MaestroService singleton with persistent AgentPool
- **TUI v2**: Modern Bubbletea v2 interface with Vim keybindings and multi-mode support
- **Sgrep Integration**: Semantic code search for intelligent code understanding
- **Shared Memory**: Configurable memory backend for cross-request context
- **File Aggregation**: 371 chunks → 21 files (proper grouping)
- **Context Enhancement**: 5 → 15+ lines of chunk context
- **Debug Visibility**: Comprehensive RAG and processing metrics
- **Lint Compliance**: Zero golangci-lint issues

## 🔬 Advanced Usage

### **Debug Mode**
```bash
# Full debug mode with all logging enabled
export MAESTRO_LOG_LEVEL=debug
export MAESTRO_RAG_DEBUG_ENABLED=true
export MAESTRO_LLM_RESPONSE_DEBUG=true
export MAESTRO_SIMILARITY_LOGGING=true
export MAESTRO_CONTEXT_EXTRACTION_ENABLED=true

./maestro --owner=user --repo=project --pr=123 --verbose
```

### **Performance Tuning**
```bash
# Optimize for speed
export MAESTRO_CHUNK_CONTEXT_LINES=10
export MAESTRO_DEDUPLICATION_THRESHOLD=0.9

# Optimize for accuracy  
export MAESTRO_CHUNK_CONTEXT_LINES=20
export MAESTRO_DEDUPLICATION_THRESHOLD=0.7
export MAESTRO_ENABLE_DEPENDENCY_ANALYSIS=true
```

## 📄 License

Maestro is released under the MIT License. See the [LICENSE](LICENSE) file for details.

---
