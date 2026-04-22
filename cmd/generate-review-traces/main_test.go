package main

import "testing"

func TestResolveConfiguredModel_UsesExplicitBaseURL(t *testing.T) {
	model, err := resolveConfiguredModel("llamacpp:qwen3.5-9b", "google", "gemini-3.0-pro", "", "", "http://127.0.0.1:8081")
	if err != nil {
		t.Fatalf("resolveConfiguredModel() error = %v", err)
	}

	if model.Config.BaseURL != "http://127.0.0.1:8081" {
		t.Fatalf("model.Config.BaseURL = %q, want %q", model.Config.BaseURL, "http://127.0.0.1:8081")
	}
	if model.ID != "llamacpp:qwen3.5-9b" {
		t.Fatalf("model.ID = %q, want %q", model.ID, "llamacpp:qwen3.5-9b")
	}
}
