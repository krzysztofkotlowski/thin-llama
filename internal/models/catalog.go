package models

import (
	"fmt"
	"sort"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
)

type Catalog struct {
	byName map[string]config.ModelConfig
}

func New(cfg *config.Config) (*Catalog, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	catalog := &Catalog{
		byName: make(map[string]config.ModelConfig, len(cfg.Models)),
	}
	for _, model := range cfg.Models {
		catalog.byName[model.Name] = model
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
