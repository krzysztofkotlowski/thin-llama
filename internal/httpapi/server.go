package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/buildinfo"
	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/metrics"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	tlruntime "github.com/krzysztofkotlowski/thin-llama/internal/runtime"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

type Runtime interface {
	Health() tlruntime.HealthSnapshot
	Snapshot() tlruntime.RuntimeSnapshot
	ChatTarget(requested string) (tlruntime.Target, error)
	EmbeddingTarget(requested string) (tlruntime.Target, error)
	SetActiveModels(ctx context.Context, chat string, embedding string) error
}

type Puller interface {
	PullModel(ctx context.Context, modelName string) (*pull.Result, error)
}

type App struct {
	cfg     *config.Config
	catalog *models.Catalog
	runtime Runtime
	puller  Puller
	store   *state.Store
	metrics *metrics.Set
	client  *http.Client
	build   buildinfo.Info
}

func NewServer(cfg *config.Config, catalog *models.Catalog, runtime Runtime, puller Puller, store *state.Store, metricSet *metrics.Set, build buildinfo.Info) http.Handler {
	app := &App{
		cfg:     cfg,
		catalog: catalog,
		runtime: runtime,
		puller:  puller,
		store:   store,
		metrics: metricSet,
		build:   build,
		client: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", app.handleHealth)
	mux.HandleFunc("/api/runtime", app.handleRuntime)
	mux.HandleFunc("/api/tags", app.handleTags)
	mux.HandleFunc("/api/models", app.handleModels)
	mux.HandleFunc("/api/models/active", app.handleActiveModels)
	mux.HandleFunc("/api/chat", app.handleChat)
	mux.HandleFunc("/api/embed", app.handleEmbed)
	mux.HandleFunc("/api/pull", app.handlePull)
	mux.Handle("/metrics", metricSet.Handler())
	return app.withMetrics(mux)
}

func (a *App) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.metrics.HTTPRequests.WithLabelValues(r.Method, r.URL.Path, http.StatusText(rec.status)).Inc()
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(r.ResponseWriter, src)
}
