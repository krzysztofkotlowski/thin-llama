package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
)

func runPull(args []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "config.json", "path to config json")
	modelName := fs.String("model", "", "configured model name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *modelName == "" {
		fmt.Fprintln(os.Stderr, "pull: --model is required")
		return 2
	}

	cfg, catalog, store, err := loadValidatedConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pull: %v\n", err)
		return 1
	}

	manager := pull.NewManager(cfg, catalog, store)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := manager.PullModel(ctx, *modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pull: %v\n", err)
		return 1
	}

	status := "already-present"
	if result.Downloaded {
		status = "downloaded"
	}
	fmt.Fprintf(
		os.Stdout,
		"model=%s status=%s path=%s checksum_verified=%t\n",
		result.Model,
		status,
		result.Path,
		result.ChecksumVerified,
	)
	return 0
}
