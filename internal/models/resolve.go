package models

import (
	"fmt"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
)

func ResolveActive(cfg *config.Config, catalog *Catalog) (config.ModelConfig, config.ModelConfig, error) {
	chat, err := ResolveForRole("", "chat", cfg, catalog)
	if err != nil {
		return config.ModelConfig{}, config.ModelConfig{}, err
	}
	embedding, err := ResolveForRole("", "embedding", cfg, catalog)
	if err != nil {
		return config.ModelConfig{}, config.ModelConfig{}, err
	}
	return chat, embedding, nil
}

func ResolveForRole(requested, role string, cfg *config.Config, catalog *Catalog) (config.ModelConfig, error) {
	name := requested
	if name == "" {
		if role == "chat" {
			name = cfg.Active.Chat
		} else {
			name = cfg.Active.Embedding
		}
	}
	if name == "" {
		return config.ModelConfig{}, fmt.Errorf("no active %s model selected", role)
	}
	model, err := catalog.Require(name)
	if err != nil {
		return config.ModelConfig{}, err
	}
	if model.Role != role {
		return config.ModelConfig{}, fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role)
	}
	return model, nil
}
