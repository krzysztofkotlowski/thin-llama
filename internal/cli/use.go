package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

var errAPINotReachable = errors.New("thin-llama api is not reachable")

func runUse(args []string) int {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to config json")
	apiBase := fs.String("api", defaultAPIBase(), "thin-llama API base URL")
	chatModel := fs.String("chat", "", "chat model to activate")
	embeddingModel := fs.String("embedding", "", "embedding model to activate")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*chatModel) == "" && strings.TrimSpace(*embeddingModel) == "" {
		fmt.Fprintln(os.Stderr, "use: at least one of --chat or --embedding is required")
		return 2
	}

	cfg, catalog, store, err := loadValidatedConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "use: %v\n", err)
		return 1
	}
	if err := validateAvailableRoleModel(cfg, catalog, "chat", strings.TrimSpace(*chatModel)); err != nil {
		fmt.Fprintf(os.Stderr, "use: %v\n", err)
		return 1
	}
	if err := validateAvailableRoleModel(cfg, catalog, "embedding", strings.TrimSpace(*embeddingModel)); err != nil {
		fmt.Fprintf(os.Stderr, "use: %v\n", err)
		return 1
	}

	if err := tryLiveActiveSwitch(*apiBase, strings.TrimSpace(*chatModel), strings.TrimSpace(*embeddingModel)); err == nil {
		fmt.Fprintln(os.Stdout, "active models updated through the running API")
		return 0
	} else if !errors.Is(err, errAPINotReachable) {
		fmt.Fprintf(os.Stderr, "use: %v\n", err)
		return 1
	}

	if _, err := store.Update(func(st *state.State) error {
		if strings.TrimSpace(*chatModel) != "" {
			st.Active.Chat = strings.TrimSpace(*chatModel)
		}
		if strings.TrimSpace(*embeddingModel) != "" {
			st.Active.Embedding = strings.TrimSpace(*embeddingModel)
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "use: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "active models persisted to state; restart thin-llama or call the HTTP API for a live switch")
	return 0
}

func tryLiveActiveSwitch(apiBase, chatModel, embeddingModel string) error {
	body, err := json.Marshal(map[string]string{
		"chat":      chatModel,
		"embedding": embeddingModel,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiBase, "/")+"/api/models/active", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("%w: %v", errAPINotReachable, urlErr)
		}
		return fmt.Errorf("%w: %v", errAPINotReachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
