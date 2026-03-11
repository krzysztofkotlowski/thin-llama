package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

type Target struct {
	Model   config.ModelConfig
	BaseURL string
	Port    int
}

type RoleHealth struct {
	Role      string `json:"role"`
	ModelName string `json:"model_name"`
	Port      int    `json:"port"`
	Running   bool   `json:"running"`
	Ready     bool   `json:"ready"`
	LastError string `json:"last_error,omitempty"`
}

type HealthSnapshot struct {
	OK        bool       `json:"ok"`
	Chat      RoleHealth `json:"chat"`
	Embedding RoleHealth `json:"embedding"`
}

type Supervisor struct {
	cfg     *config.Config
	catalog *models.Catalog
	store   *state.Store

	mu       sync.RWMutex
	process  map[string]*ManagedProcess
	stopping bool
	wg       sync.WaitGroup
}

func NewSupervisor(cfg *config.Config, catalog *models.Catalog, store *state.Store) *Supervisor {
	return &Supervisor{
		cfg:     cfg,
		catalog: catalog,
		store:   store,
		process: make(map[string]*ManagedProcess, 2),
	}
}

func (s *Supervisor) Start(ctx context.Context) error {
	chat, embedding, err := models.ResolveActive(s.cfg, s.catalog)
	if err != nil {
		return err
	}

	chatPort, embeddingPort, err := ResolvePorts(chat.Port, embedding.Port)
	if err != nil {
		return err
	}

	if err := s.startRole(ctx, "chat", chat, chatPort); err != nil {
		return err
	}
	if err := s.startRole(ctx, "embedding", embedding, embeddingPort); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Stop(stopCtx)
		return err
	}

	_, err = s.store.Update(func(st *state.State) error {
		st.Active.Chat = chat.Name
		st.Active.Embedding = embedding.Name
		return nil
	})
	return err
}

func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	processes := make([]*ManagedProcess, 0, len(s.process))
	for _, proc := range s.process {
		processes = append(processes, proc)
	}
	s.mu.Unlock()

	var firstErr error
	for _, proc := range processes {
		if err := proc.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.wg.Wait()

	_, _ = s.store.Update(func(st *state.State) error {
		for role, process := range st.Processes {
			process.Running = false
			process.LastExitAt = nowString()
			st.Processes[role] = process
		}
		return nil
	})
	return firstErr
}

func (s *Supervisor) Health() HealthSnapshot {
	s.mu.RLock()
	chatProc := s.process["chat"]
	embeddingProc := s.process["embedding"]
	s.mu.RUnlock()

	chat := buildRoleHealth("chat", chatProc)
	embedding := buildRoleHealth("embedding", embeddingProc)
	return HealthSnapshot{
		OK:        chat.Ready && embedding.Ready,
		Chat:      chat,
		Embedding: embedding,
	}
}

func (s *Supervisor) ChatTarget(requested string) (Target, error) {
	return s.targetForRole("chat", requested)
}

func (s *Supervisor) EmbeddingTarget(requested string) (Target, error) {
	return s.targetForRole("embedding", requested)
}

func (s *Supervisor) startRole(ctx context.Context, role string, model config.ModelConfig, port int) error {
	modelPath := pull.ResolveModelPath(s.cfg, model)
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("%s model %q is not available at %s: %w", role, model.Name, modelPath, err)
	}

	proc := NewManagedProcess(role, model, modelPath, s.cfg.LlamaServerBin, port)
	if err := proc.Start(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.process[role] = proc
	s.mu.Unlock()

	_, err := s.store.Update(func(st *state.State) error {
		st.Models[model.Name] = state.ModelState{
			Name:           model.Name,
			LocalPath:      modelPath,
			Available:      true,
			LastChecksumOK: st.Models[model.Name].LastChecksumOK,
			UpdatedAt:      nowString(),
		}
		st.Processes[role] = state.ProcessState{
			Role:          role,
			ModelName:     model.Name,
			Port:          port,
			PID:           proc.PID(),
			Running:       true,
			LastStartedAt: proc.LastStartedAt().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.watchProcess(role, proc)
	return nil
}

func (s *Supervisor) watchProcess(role string, proc *ManagedProcess) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		done := proc.WaitChan()
		if done == nil {
			return
		}
		err, ok := <-done
		if !ok {
			err = nil
		}
		proc.MarkExited(err)
		s.recordExit(role, proc, err)
		if proc.WasStopped() || s.isStopping() {
			return
		}
		s.restartLoop(role, proc.Model(), proc.Port())
	}()
}

func (s *Supervisor) restartLoop(role string, model config.ModelConfig, port int) {
	for {
		if s.isStopping() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := s.startRole(ctx, role, model, port)
		cancel()
		if err == nil {
			return
		}
		_, _ = s.store.Update(func(st *state.State) error {
			current := st.Processes[role]
			current.Role = role
			current.ModelName = model.Name
			current.Port = port
			current.Running = false
			current.LastError = err.Error()
			current.LastExitAt = nowString()
			st.Processes[role] = current
			return nil
		})
		time.Sleep(2 * time.Second)
	}
}

func (s *Supervisor) targetForRole(role, requested string) (Target, error) {
	model, err := models.ResolveForRole(requested, role, s.cfg, s.catalog)
	if err != nil {
		return Target{}, err
	}

	activeName := s.cfg.Active.Embedding
	if role == "chat" {
		activeName = s.cfg.Active.Chat
	}
	if model.Name != activeName {
		return Target{}, fmt.Errorf("model %q is configured but not active for role %q", model.Name, role)
	}

	s.mu.RLock()
	proc := s.process[role]
	s.mu.RUnlock()
	if proc == nil || !proc.Ready() {
		return Target{}, fmt.Errorf("%s runtime is not ready", role)
	}
	return Target{
		Model:   proc.Model(),
		BaseURL: proc.BaseURL(),
		Port:    proc.Port(),
	}, nil
}

func (s *Supervisor) recordExit(role string, proc *ManagedProcess, err error) {
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.Role = role
		current.ModelName = proc.Model().Name
		current.Port = proc.Port()
		current.PID = proc.PID()
		current.Running = false
		current.LastExitAt = nowString()
		if err != nil {
			current.LastError = err.Error()
		}
		st.Processes[role] = current
		return nil
	})
}

func (s *Supervisor) isStopping() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopping
}

func buildRoleHealth(role string, proc *ManagedProcess) RoleHealth {
	if proc == nil {
		return RoleHealth{
			Role: role,
		}
	}
	ready := proc.Running() && checkPort(proc.Port())
	return RoleHealth{
		Role:      role,
		ModelName: proc.Model().Name,
		Port:      proc.Port(),
		Running:   proc.Running(),
		Ready:     ready,
		LastError: proc.LastError(),
	}
}

func checkPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
