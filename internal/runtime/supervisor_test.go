package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

func TestSupervisorStartAndStopWithFakeBinary(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for fake llama-server test")
	}

	dir := t.TempDir()
	chatModel := filepath.Join(dir, "chat.gguf")
	embedModel := filepath.Join(dir, "embed.gguf")
	if err := os.WriteFile(chatModel, []byte("chat"), 0o644); err != nil {
		t.Fatalf("WriteFile(chat) unexpected error: %v", err)
	}
	if err := os.WriteFile(embedModel, []byte("embed"), 0o644); err != nil {
		t.Fatalf("WriteFile(embed) unexpected error: %v", err)
	}

	fakeBinary := filepath.Join(dir, "fake-llama-server.sh")
	script := `#!/bin/sh
PORT=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --port)
      PORT="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
exec python3 -u -c '
import signal, socket, sys
port = int(sys.argv[1])
server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("127.0.0.1", port))
server.listen(5)
running = True
def stop(*args):
    global running
    running = False
signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
while running:
    server.settimeout(0.2)
    try:
        conn, _ = server.accept()
        conn.close()
    except Exception:
        pass
server.close()
' "$PORT"
`
	if err := os.WriteFile(fakeBinary, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake binary) unexpected error: %v", err)
	}

	cfg := &config.Config{
		ListenAddr:     ":8080",
		StateDir:       filepath.Join(dir, "state"),
		ModelsDir:      dir,
		LlamaServerBin: fakeBinary,
		Active: config.ActiveModels{
			Chat:      "chat-model",
			Embedding: "embed-model",
		},
		Models: []config.ModelConfig{
			{Name: "chat-model", Role: "chat", GGUFPath: chatModel, Port: 12435},
			{Name: "embed-model", Role: "embedding", GGUFPath: embedModel, EmbeddingDims: 384, Port: 12436},
		},
	}
	catalog, err := models.New(cfg)
	if err != nil {
		t.Fatalf("models.New() unexpected error: %v", err)
	}

	supervisor := NewSupervisor(cfg, catalog, state.New(cfg.StateDir))
	startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := supervisor.Start(startCtx); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}

	health := supervisor.Health()
	if !health.OK {
		t.Fatalf("Health().OK = false: %+v", health)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := supervisor.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() unexpected error: %v", err)
	}
}
