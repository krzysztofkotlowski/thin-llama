package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/httpapi"
	"github.com/krzysztofkotlowski/thin-llama/internal/metrics"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	tlruntime "github.com/krzysztofkotlowski/thin-llama/internal/runtime"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func Run(args []string, build BuildInfo) int {
	if len(args) == 0 {
		printRootUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "pull":
		return runPull(args[1:])
	case "use":
		return runUse(args[1:])
	case "models":
		return runModels(args[1:])
	case "validate-config":
		return runValidateConfig(args[1:])
	case "version":
		fmt.Fprintf(os.Stdout, "thin-llama %s (%s, %s)\n", build.Version, build.Commit, build.Date)
		return 0
	case "help", "-h", "--help":
		printRootUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printRootUsage(os.Stderr)
		return 2
	}
}

func printRootUsage(w *os.File) {
	fmt.Fprintln(w, "thin-llama")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  thin-llama serve --config ./config.local.json")
	fmt.Fprintln(w, "  thin-llama pull --config ./config.local.json --model <name>")
	fmt.Fprintln(w, "  thin-llama use --config ./config.local.json --chat <name> --embedding <name>")
	fmt.Fprintln(w, "  thin-llama models --config ./config.local.json")
	fmt.Fprintln(w, "  thin-llama validate-config --config ./config.local.json")
	fmt.Fprintln(w, "  thin-llama version")
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to config json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, catalog, store, err := loadValidatedConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	metricSet := metrics.New()
	manager := pull.NewManager(cfg, catalog, store)
	supervisor := tlruntime.NewSupervisor(cfg, catalog, store)
	supervisor.Start(context.Background())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = supervisor.Stop(ctx)
	}()

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.NewServer(cfg, catalog, supervisor, manager, store, metricSet),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "serve: received %s, shutting down\n", sig)
	case err := <-serverErr:
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: shutdown failed: %v\n", err)
		return 1
	}
	if err := supervisor.Stop(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: model shutdown failed: %v\n", err)
		return 1
	}
	return 0
}
