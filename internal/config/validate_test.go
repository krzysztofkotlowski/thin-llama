package config

import "testing"

func validConfig() *Config {
	return &Config{
		ListenAddr:     ":8080",
		StateDir:       "/state",
		ModelsDir:      "/models",
		LlamaServerBin: "/usr/local/bin/llama-server",
		Active: ActiveModels{
			Chat:      "chat-model",
			Embedding: "embed-model",
		},
		Models: []ModelConfig{
			{Name: "chat-model", Role: "chat", GGUFPath: "/models/chat.gguf"},
			{Name: "embed-model", Role: "embedding", GGUFPath: "/models/embed.gguf", EmbeddingDims: 384},
		},
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestValidateRejectsDuplicateModelNames(t *testing.T) {
	cfg := validConfig()
	cfg.Models = append(cfg.Models, ModelConfig{Name: "chat-model", Role: "chat", GGUFPath: "/models/other.gguf"})

	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() expected duplicate model error")
	}
}

func TestValidateRejectsMissingEmbeddingDims(t *testing.T) {
	cfg := validConfig()
	cfg.Models[1].EmbeddingDims = 0

	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() expected missing embedding dims error")
	}
}

func TestValidateRejectsMissingActiveRole(t *testing.T) {
	cfg := validConfig()
	cfg.Active.Embedding = "missing"

	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() expected missing active embedding error")
	}
}

func TestValidateRejectsMissingModelSourceAndPath(t *testing.T) {
	cfg := validConfig()
	cfg.Models[0].GGUFPath = ""
	cfg.Models[0].SourceURL = ""

	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() expected model source/path error")
	}
}
