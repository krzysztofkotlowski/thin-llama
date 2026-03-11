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
