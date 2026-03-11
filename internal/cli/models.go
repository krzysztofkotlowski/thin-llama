package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
)

func runModels(args []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to config json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, catalog, store, err := loadValidatedConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return 1
	}

	current, err := store.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return 1
	}
	activeChat := current.Active.Chat
	if activeChat == "" {
		activeChat = cfg.Active.Chat
	}
	activeEmbedding := current.Active.Embedding
	if activeEmbedding == "" {
		activeEmbedding = cfg.Active.Embedding
	}

	fmt.Fprintf(os.Stdout, "active.chat=%s\n", activeChat)
	fmt.Fprintf(os.Stdout, "active.embedding=%s\n", activeEmbedding)
	for _, model := range catalog.All() {
		localPath := pull.ResolveModelPath(cfg, model)
		status := "missing"
		if _, err := os.Stat(localPath); err == nil {
			status = "available"
		}
		active := "false"
		if (model.Role == "chat" && model.Name == activeChat) || (model.Role == "embedding" && model.Name == activeEmbedding) {
			active = "true"
		}
		fmt.Fprintf(os.Stdout, "%s role=%s path=%s status=%s active=%s\n", model.Name, model.Role, localPath, status, active)
	}
	return 0
}
