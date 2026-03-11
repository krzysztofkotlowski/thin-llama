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
	configPath := fs.String("config", "config.json", "path to config json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, catalog, _, err := loadValidatedConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "active.chat=%s\n", cfg.Active.Chat)
	fmt.Fprintf(os.Stdout, "active.embedding=%s\n", cfg.Active.Embedding)
	for _, model := range catalog.All() {
		localPath := pull.ResolveModelPath(cfg, model)
		status := "missing"
		if _, err := os.Stat(localPath); err == nil {
			status = "available"
		}
		fmt.Fprintf(os.Stdout, "%s role=%s path=%s status=%s\n", model.Name, model.Role, localPath, status)
	}
	return 0
}
