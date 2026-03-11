package config

import "time"

type Config struct {
	ListenAddr            string        `json:"listen_addr"`
	StateDir              string        `json:"state_dir"`
	ModelsDir             string        `json:"models_dir"`
	LlamaServerBin        string        `json:"llama_server_bin"`
	StartupTimeoutSeconds int           `json:"startup_timeout_seconds,omitempty"`
	Active                ActiveModels  `json:"active"`
	Models                []ModelConfig `json:"models"`
}

type ActiveModels struct {
	Chat      string `json:"chat"`
	Embedding string `json:"embedding"`
}

type ModelConfig struct {
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	GGUFPath      string   `json:"gguf_path"`
	SourceURL     string   `json:"source_url,omitempty"`
	SHA256        string   `json:"sha256,omitempty"`
	EmbeddingDims int      `json:"embedding_dims,omitempty"`
	Threads       int      `json:"threads,omitempty"`
	ContextSize   int      `json:"context_size,omitempty"`
	GPULayers     int      `json:"gpu_layers,omitempty"`
	ExtraArgs     []string `json:"extra_args,omitempty"`
	Port          int      `json:"port,omitempty"`
}

func (c *Config) StartupTimeout() time.Duration {
	if c == nil || c.StartupTimeoutSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.StartupTimeoutSeconds) * time.Second
}
