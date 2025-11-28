.PHONY: build clean run test deps setup local stop-local kill

# Build maestro with suppressed SQLite warnings
build:
	@echo "Building maestro..."
	@CGO_CFLAGS="-Wno-deprecated-declarations" go build -o maestro

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f maestro

# Run maestro in interactive mode
run:
	@go run . --owner=XiaoConstantine --repo=dspy-go --model=google:gemini-2.5-flash 2>&1 | tee dev.log

# Run tests
test:
	@echo "Running tests..."
	@CGO_CFLAGS="-Wno-deprecated-declarations" go test ./...

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod tidy

# Setup: Install llama.cpp and download models
setup:
	@echo "Setting up local LLM environment..."
	@mkdir -p ~/.maestro/models
	@echo "Models directory created at ~/.maestro/models"
	@command -v llama-server >/dev/null 2>&1 || (echo "Installing llama.cpp..." && brew install llama.cpp)
	@echo "Downloading nomic-embed-text-v1.5.Q8_0.gguf model (~140MB)..."
	@if [ ! -f ~/.maestro/models/nomic-embed-text-v1.5.Q8_0.gguf ] || [ $$(stat -f%z ~/.maestro/models/nomic-embed-text-v1.5.Q8_0.gguf 2>/dev/null || echo 0) -lt 1000000 ]; then \
		curl -L --progress-bar -o ~/.maestro/models/nomic-embed-text-v1.5.Q8_0.gguf \
			"https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.Q8_0.gguf"; \
	else \
		echo "nomic-embed-text-v1.5.Q8_0.gguf already exists"; \
	fi
	@echo "Downloading Qwen3-1.7B-Q8_0.gguf model (~1.8GB)..."
	@if [ ! -f ~/.maestro/models/Qwen3-1.7B-Q8_0.gguf ] || [ $$(stat -f%z ~/.maestro/models/Qwen3-1.7B-Q8_0.gguf 2>/dev/null || echo 0) -lt 1000000 ]; then \
		curl -L --progress-bar -o ~/.maestro/models/Qwen3-1.7B-Q8_0.gguf \
			"https://huggingface.co/Qwen/Qwen3-1.7B-GGUF/resolve/main/Qwen3-1.7B-Q8_0.gguf"; \
	else \
		echo "Qwen3-1.7B-Q8_0.gguf already exists"; \
	fi
	@echo "Setup complete! Models are in ~/.maestro/models"
	@echo "Run 'make local' to start the servers"

# Start local llama servers in tmux sessions
local:
	@echo "Starting local LLM servers..."
	@if ! [ -f ~/.maestro/models/nomic-embed-text-v1.5.Q8_0.gguf ]; then \
		echo "Error: Models not found. Run 'make setup' first."; \
		exit 1; \
	fi
	@tmux kill-session -t embedding 2>/dev/null || true
	@tmux kill-session -t llm 2>/dev/null || true
	@tmux new-session -d -s embedding "llama-server -m ~/.maestro/models/nomic-embed-text-v1.5.Q8_0.gguf --port 8080 -c 8192 -b 8192 --ubatch-size 8192 --n-gpu-layers 99 --embedding --pooling mean"
	@echo "Embedding server started in tmux session 'embedding' on port 8080"
	@tmux new-session -d -s llm "llama-server -m ~/.maestro/models/Qwen3-1.7B-Q8_0.gguf --port 8081 -c 8192 --n-gpu-layers 99"
	@echo "LLM server started in tmux session 'llm' on port 8081"
	@echo ""
	@echo "Use 'tmux attach -t embedding' or 'tmux attach -t llm' to view logs"
	@echo "Use 'make stop-local' to stop the servers"

# Stop local llama servers
stop-local:
	@echo "Stopping local LLM servers..."
	@tmux kill-session -t embedding 2>/dev/null && echo "Stopped embedding server" || echo "Embedding server not running"
	@tmux kill-session -t llm 2>/dev/null && echo "Stopped LLM server" || echo "LLM server not running"

# Kill running maestro process
kill:
	@pkill -f "./maestro" 2>/dev/null && echo "Killed maestro process" || echo "No maestro process running"

# Default target
all: build
