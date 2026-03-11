package config

import (
	"fmt"
	"strings"
)

func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if strings.TrimSpace(cfg.StateDir) == "" {
		return fmt.Errorf("state_dir is required")
	}
	if strings.TrimSpace(cfg.ModelsDir) == "" {
		return fmt.Errorf("models_dir is required")
	}
	if strings.TrimSpace(cfg.LlamaServerBin) == "" {
		return fmt.Errorf("llama_server_bin is required")
	}
	if strings.TrimSpace(cfg.Active.Chat) == "" {
		return fmt.Errorf("active.chat is required")
	}
	if strings.TrimSpace(cfg.Active.Embedding) == "" {
		return fmt.Errorf("active.embedding is required")
	}
	if len(cfg.Models) == 0 {
		return fmt.Errorf("at least one model must be configured")
	}

	seen := make(map[string]ModelConfig, len(cfg.Models))
	for _, model := range cfg.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			return fmt.Errorf("model name is required")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate model name: %s", name)
		}
		switch strings.TrimSpace(model.Role) {
		case "chat":
		case "embedding":
			if model.EmbeddingDims <= 0 {
				return fmt.Errorf("embedding model %s requires positive embedding_dims", name)
			}
		default:
			return fmt.Errorf("model %s has invalid role %q", name, model.Role)
		}
		if strings.TrimSpace(model.GGUFPath) == "" && strings.TrimSpace(model.SourceURL) == "" {
			return fmt.Errorf("model %s requires gguf_path or source_url", name)
		}
		seen[name] = model
	}

	chat, ok := seen[cfg.Active.Chat]
	if !ok {
		return fmt.Errorf("active.chat %q is not configured", cfg.Active.Chat)
	}
	if chat.Role != "chat" {
		return fmt.Errorf("active.chat %q is not a chat model", cfg.Active.Chat)
	}
	embedding, ok := seen[cfg.Active.Embedding]
	if !ok {
		return fmt.Errorf("active.embedding %q is not configured", cfg.Active.Embedding)
	}
	if embedding.Role != "embedding" {
		return fmt.Errorf("active.embedding %q is not an embedding model", cfg.Active.Embedding)
	}
	return nil
}
