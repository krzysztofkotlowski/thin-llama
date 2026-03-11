package models

import (
	_ "embed"
	"fmt"
	"encoding/json"
	"sort"
	"strings"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
)

//go:embed builtin_catalog.json
var builtinCatalogBytes []byte

type Catalog struct {
	byName map[string]config.ModelConfig
}

func New(cfg *config.Config) (*Catalog, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}

	var builtin []config.ModelConfig
	if err := json.Unmarshal(builtinCatalogBytes, &builtin); err != nil {
		return nil, fmt.Errorf("parse built-in catalog: %w", err)
	}

	catalog := &Catalog{
		byName: make(map[string]config.ModelConfig, len(builtin)+len(cfg.Models)),
	}
	for _, model := range builtin {
		catalog.byName[model.Name] = model
	}
	for _, model := range cfg.Models {
		catalog.byName[model.Name] = model
	}

	if active := strings.TrimSpace(cfg.Active.Chat); active != "" {
		model, ok := catalog.byName[active]
		if !ok {
			return nil, fmt.Errorf("active.chat %q is not in the merged catalog", active)
		}
		if model.Role != "chat" {
			return nil, fmt.Errorf("active.chat %q is not a chat model", active)
		}
	}
	if active := strings.TrimSpace(cfg.Active.Embedding); active != "" {
		model, ok := catalog.byName[active]
		if !ok {
			return nil, fmt.Errorf("active.embedding %q is not in the merged catalog", active)
		}
		if model.Role != "embedding" {
			return nil, fmt.Errorf("active.embedding %q is not an embedding model", active)
		}
	}
	return catalog, nil
}

func (c *Catalog) All() []config.ModelConfig {
	out := make([]config.ModelConfig, 0, len(c.byName))
	for _, model := range c.byName {
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *Catalog) Get(name string) (config.ModelConfig, bool) {
	model, ok := c.byName[name]
	return model, ok
}

func (c *Catalog) Require(name string) (config.ModelConfig, error) {
	model, ok := c.Get(name)
	if !ok {
		return config.ModelConfig{}, fmt.Errorf("model %q is not configured", name)
	}
	return model, nil
}

func (c *Catalog) ByRole(role string) []config.ModelConfig {
	out := make([]config.ModelConfig, 0)
	for _, model := range c.byName {
		if model.Role == role {
			out = append(out, model)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
