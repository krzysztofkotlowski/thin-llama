package models

import (
	"testing"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
		Active: config.ActiveModels{
			Chat:      "chat-model",
			Embedding: "embed-model",
		},
		Models: []config.ModelConfig{
			{Name: "chat-model", Role: "chat", GGUFPath: "/models/chat.gguf"},
			{Name: "embed-model", Role: "embedding", GGUFPath: "/models/embed.gguf", EmbeddingDims: 384},
		},
	}
}

func TestNewCatalogAndResolveByRole(t *testing.T) {
	catalog, err := New(testConfig())
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	chat, err := ResolveForRole("", "chat", testConfig(), catalog)
	if err != nil {
		t.Fatalf("ResolveForRole(chat) unexpected error: %v", err)
	}
	if chat.Name != "chat-model" {
		t.Fatalf("ResolveForRole(chat) = %q", chat.Name)
	}

	embed, err := ResolveForRole("", "embedding", testConfig(), catalog)
	if err != nil {
		t.Fatalf("ResolveForRole(embedding) unexpected error: %v", err)
	}
	if embed.Name != "embed-model" {
		t.Fatalf("ResolveForRole(embedding) = %q", embed.Name)
	}
}

func TestResolveForRoleRejectsWrongRole(t *testing.T) {
	catalog, err := New(testConfig())
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if _, err := ResolveForRole("embed-model", "chat", testConfig(), catalog); err == nil {
		t.Fatal("ResolveForRole() expected wrong-role error")
	}
}

func TestNewLoadsBuiltInCatalogWithoutExplicitModels(t *testing.T) {
	cfg := &config.Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
		Models:         nil,
	}

	catalog, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if _, ok := catalog.Get("qwen2.5:3b"); !ok {
		t.Fatal("expected built-in chat model qwen2.5:3b")
	}
	if _, ok := catalog.Get("all-minilm"); !ok {
		t.Fatal("expected built-in embedding model all-minilm")
	}
}

func TestBuiltInCatalogUsesConservativeHomeServerDefaults(t *testing.T) {
	cfg := &config.Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
	}

	catalog, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	chat, ok := catalog.Get("qwen2.5:3b")
	if !ok {
		t.Fatal("expected built-in qwen2.5:3b model")
	}
	if chat.ContextSize != 512 {
		t.Fatalf("chat.ContextSize = %d, want 512", chat.ContextSize)
	}

	embed, ok := catalog.Get("all-minilm")
	if !ok {
		t.Fatal("expected built-in all-minilm model")
	}
	if embed.EmbeddingDims != 384 {
		t.Fatalf("embed.EmbeddingDims = %d, want 384", embed.EmbeddingDims)
	}

	if containsArg(chat.ExtraArgs, "--no-cache-prompt") || containsArg(embed.ExtraArgs, "--no-cache-prompt") {
		t.Fatalf("expected conservative catalog to disable prompt cache via cache-ram only: chat=%v embed=%v", chat.ExtraArgs, embed.ExtraArgs)
	}
	if !hasArgValue(chat.ExtraArgs, "--cache-ram", "0") {
		t.Fatalf("expected qwen2.5:3b cache-ram=0, got %v", chat.ExtraArgs)
	}
	if !hasArgValue(embed.ExtraArgs, "--cache-ram", "0") {
		t.Fatalf("expected all-minilm cache-ram=0, got %v", embed.ExtraArgs)
	}
}

func TestConfigModelsOverrideBuiltInCatalogByName(t *testing.T) {
	cfg := &config.Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
		Models: []config.ModelConfig{
			{
				Name:          "all-minilm",
				Role:          "embedding",
				GGUFPath:      "custom/all-minilm.gguf",
				SourceURL:     "https://example.com/custom-minilm.gguf",
				SHA256:        "abc123",
				EmbeddingDims: 384,
			},
		},
	}

	catalog, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	model, ok := catalog.Get("all-minilm")
	if !ok {
		t.Fatal("expected overridden all-minilm model")
	}
	if model.GGUFPath != "custom/all-minilm.gguf" || model.SHA256 != "abc123" {
		t.Fatalf("override not applied: %+v", model)
	}
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func hasArgValue(args []string, flag string, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
