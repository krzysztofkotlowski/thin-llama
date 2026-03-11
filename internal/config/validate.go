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
	return nil
}
