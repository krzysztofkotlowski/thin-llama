package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

const defaultConfigPath = "config.local.json"

func defaultAPIBase() string {
	value := strings.TrimSpace(os.Getenv("THIN_LLAMA_API"))
	if value == "" {
		return "http://127.0.0.1:8080"
	}
	return strings.TrimRight(value, "/")
}

func loadValidatedConfig(path string) (*config.Config, *models.Catalog, *state.Store, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := config.Validate(cfg); err != nil {
		return nil, nil, nil, err
	}
	catalog, err := models.New(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, catalog, state.New(cfg.StateDir), nil
}

func validateAvailableRoleModel(cfg *config.Config, catalog *models.Catalog, role, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	model, err := catalog.Require(name)
	if err != nil {
		return err
	}
	if model.Role != role {
		return fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role)
	}
	path := pull.ResolveModelPath(cfg, model)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("model %q is not downloaded at %s; pull it first", model.Name, path)
	}
	return nil
}
