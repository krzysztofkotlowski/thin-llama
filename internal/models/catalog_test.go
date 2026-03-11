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
