# Integration Tests

This directory contains integration tests for Maestro that require real API keys and make actual calls to LLM providers.

## Prerequisites

Set the following environment variables:

```bash
export ANTHROPIC_API_KEY="your-anthropic-api-key"
export GOOGLE_API_KEY="your-google-api-key"
# or
export GEMINI_API_KEY="your-gemini-api-key"
```

## Running Tests

### Run all integration tests

```bash
go test ./tests/integration/... -v -tags=integration -timeout=5m
```

### Run specific test

```bash
go test ./tests/integration/... -v -tags=integration -run TestLive_SimpleContextPass
```

### Run with inline environment variables

```bash
ANTHROPIC_API_KEY=sk-ant-xxx GOOGLE_API_KEY=xxx \
  go test ./tests/integration/... -v -tags=integration -timeout=5m
```

## Test Scenarios

### 1. Simple Context Pass (`TestLive_SimpleContextPass`)
Tests basic context sharing between Claude and Gemini:
- Claude stores a unique code (PHOENIX-2024)
- Gemini retrieves it from the shared context
- Verifies context.md contains both interactions

### 2. Multi-Turn Conversation (`TestLive_MultiTurnConversation`)
Tests accumulating context across 4 alternating exchanges:
- Turn 1: Claude notes chi router usage
- Turn 2: Gemini asks about the router
- Turn 3: Claude adds sqlx info
- Turn 4: Gemini summarizes all technical details

### 3. Context Persistence (`TestLive_ContextPersistenceAcrossRestart`)
Tests that context survives service re-initialization:
- Phase A: Claude stores API version info
- Processors go out of scope (simulates shutdown)
- Phase B: New Gemini processor retrieves the info

## Notes

- Tests are skipped if API keys are not set
- Each test uses a temporary directory for isolation
- Timeout is set to 5 minutes to account for API latency
- Tests log responses for debugging purposes
