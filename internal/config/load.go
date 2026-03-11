package config

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	defaultListenAddr            = ":8080"
	defaultStateDir              = "./state"
	defaultModelsDir             = "./models"
	defaultServerBin             = "llama-server"
	defaultStartupTimeoutSeconds = 60
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaultListenAddr
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultStateDir
	}
	if cfg.ModelsDir == "" {
		cfg.ModelsDir = defaultModelsDir
	}
	if cfg.LlamaServerBin == "" {
		cfg.LlamaServerBin = defaultServerBin
	}
	if cfg.StartupTimeoutSeconds <= 0 {
		cfg.StartupTimeoutSeconds = defaultStartupTimeoutSeconds
	}

	applyEnvOverride(&cfg.ListenAddr, "THIN_LLAMA_LISTEN_ADDR")
	applyEnvOverride(&cfg.StateDir, "THIN_LLAMA_STATE_DIR")
	applyEnvOverride(&cfg.ModelsDir, "THIN_LLAMA_MODELS_DIR")
	applyEnvOverride(&cfg.LlamaServerBin, "THIN_LLAMA_LLAMA_SERVER_BIN")

	return cfg, nil
}

func applyEnvOverride(target *string, key string) {
	if value := os.Getenv(key); value != "" {
		*target = value
	}
}
