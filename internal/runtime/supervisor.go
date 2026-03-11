package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/models"
	"github.com/krzysztofkotlowski/thin-llama/internal/pull"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

const (
	rapidFailureThreshold = 15 * time.Second
	rapidFailureWindow    = 2 * time.Minute
	rapidFailureLimit     = 3
	restartDelay          = 2 * time.Second
)

type Target struct {
	Model   config.ModelConfig
	BaseURL string
	Port    int
}

type RoleHealth struct {
	Role              string `json:"role"`
	ModelName         string `json:"model_name,omitempty"`
	Port              int    `json:"port"`
	Running           bool   `json:"running"`
	Ready             bool   `json:"ready"`
	LastError         string `json:"last_error,omitempty"`
	RestartCount      int    `json:"restart_count,omitempty"`
	RestartSuppressed bool   `json:"restart_suppressed,omitempty"`
}

type HealthSnapshot struct {
	OK           bool       `json:"ok"`
	RuntimeReady bool       `json:"runtime_ready"`
	Chat         RoleHealth `json:"chat"`
	Embedding    RoleHealth `json:"embedding"`
}

type restartTracker struct {
	Count        int
	FirstFailure time.Time
	Suppressed   bool
	SuppressedAt time.Time
}

type Supervisor struct {
	cfg     *config.Config
	catalog *models.Catalog
	store   *state.Store

	mu       sync.RWMutex
	process  map[string]*ManagedProcess
	failures map[string]restartTracker
	stopping bool
	wg       sync.WaitGroup
}

func NewSupervisor(cfg *config.Config, catalog *models.Catalog, store *state.Store) *Supervisor {
	return &Supervisor{
		cfg:      cfg,
		catalog:  catalog,
		store:    store,
		process:  make(map[string]*ManagedProcess, 2),
		failures: make(map[string]restartTracker, 2),
	}
}

func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = false
	s.mu.Unlock()

	current, err := s.store.Update(func(st *state.State) error {
		if st.Active.Chat == "" && strings.TrimSpace(s.cfg.Active.Chat) != "" {
			st.Active.Chat = strings.TrimSpace(s.cfg.Active.Chat)
		}
		if st.Active.Embedding == "" && strings.TrimSpace(s.cfg.Active.Embedding) != "" {
			st.Active.Embedding = strings.TrimSpace(s.cfg.Active.Embedding)
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.startRoleIfPossible(ctx, "chat", current.Active.Chat)
	s.startRoleIfPossible(ctx, "embedding", current.Active.Embedding)
	return nil
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
	current, err := s.store.Load()
	if err != nil {
		return HealthSnapshot{
			OK: true,
			Chat: RoleHealth{
				Role:      "chat",
				LastError: err.Error(),
			},
			Embedding: RoleHealth{
				Role:      "embedding",
				LastError: err.Error(),
			},
		}
	}

	s.mu.RLock()
	chatProc := s.process["chat"]
	embeddingProc := s.process["embedding"]
	s.mu.RUnlock()

	chat := buildRoleHealth("chat", current.Active.Chat, current.Processes["chat"], chatProc)
	embedding := buildRoleHealth("embedding", current.Active.Embedding, current.Processes["embedding"], embeddingProc)
	return HealthSnapshot{
		OK:           true,
		RuntimeReady: chat.Ready && embedding.Ready,
		Chat:         chat,
		Embedding:    embedding,
	}
}

func (s *Supervisor) ChatTarget(requested string) (Target, error) {
	return s.targetForRole("chat", requested)
}

func (s *Supervisor) EmbeddingTarget(requested string) (Target, error) {
	return s.targetForRole("embedding", requested)
}

func (s *Supervisor) SetActiveModels(ctx context.Context, chatName, embeddingName string) error {
	if strings.TrimSpace(chatName) == "" && strings.TrimSpace(embeddingName) == "" {
		return fmt.Errorf("at least one active model must be provided")
	}

	if chatName != "" {
		if err := validateRoleSelection(s.cfg, s.catalog, "chat", chatName); err != nil {
			return err
		}
	}
	if embeddingName != "" {
		if err := validateRoleSelection(s.cfg, s.catalog, "embedding", embeddingName); err != nil {
			return err
		}
	}

	if chatName != "" {
		if err := s.switchRole(ctx, "chat", chatName); err != nil {
			return err
		}
	}
	if embeddingName != "" {
		if err := s.switchRole(ctx, "embedding", embeddingName); err != nil {
			return err
		}
	}
	return nil
}

func (s *Supervisor) startRoleIfPossible(ctx context.Context, role, activeName string) {
	activeName = strings.TrimSpace(activeName)
	if activeName == "" {
		return
	}
	model, err := s.catalog.Require(activeName)
	if err != nil {
		s.recordRoleError(role, activeName, rolePort(role, config.ModelConfig{}), err)
		return
	}
	if model.Role != role {
		s.recordRoleError(role, activeName, rolePort(role, model), fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role))
		return
	}
	if err := s.startRole(ctx, role, model, rolePort(role, model)); err != nil {
		s.recordRoleError(role, model.Name, rolePort(role, model), err)
	}
}

func (s *Supervisor) switchRole(ctx context.Context, role, name string) error {
	model, err := s.catalog.Require(name)
	if err != nil {
		return err
	}
	if model.Role != role {
		return fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role)
	}
	path := pull.ResolveModelPath(s.cfg, model)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("model %q is not downloaded at %s; pull it first", model.Name, path)
	}

	s.clearRestartState(role)

	oldProc := s.currentProcess(role)
	var oldModel config.ModelConfig
	var oldPort int
	if oldProc != nil {
		oldModel = oldProc.Model()
		oldPort = oldProc.Port()
		if err := oldProc.Stop(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.process, role)
		s.mu.Unlock()
	}

	port := rolePort(role, model)
	if err := s.startRole(ctx, role, model, port); err != nil {
		s.recordRoleError(role, model.Name, port, err)
		if oldProc != nil {
			_ = s.startRole(ctx, role, oldModel, oldPort)
		}
		return err
	}

	_, err = s.store.Update(func(st *state.State) error {
		if role == "chat" {
			st.Active.Chat = model.Name
		} else {
			st.Active.Embedding = model.Name
		}
		return nil
	})
	return err
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
	s.failures[role] = restartTracker{}
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
			Role:                role,
			ModelName:           model.Name,
			Port:                port,
			PID:                 proc.PID(),
			Running:             true,
			LastStartedAt:       proc.LastStartedAt().Format(time.RFC3339Nano),
			LastError:           "",
			RestartCount:        0,
			RestartSuppressed:   false,
			RestartSuppressedAt: "",
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
		suppressed := s.recordExit(role, proc, err)
		if proc.WasStopped() || s.isStopping() || suppressed {
			return
		}
		s.restartLoop(role, proc.Model(), proc.Port())
	}()
}

func (s *Supervisor) restartLoop(role string, model config.ModelConfig, port int) {
	for {
		if s.isStopping() || s.isRestartSuppressed(role) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := s.startRole(ctx, role, model, port)
		cancel()
		if err == nil {
			return
		}
		if s.recordRestartFailure(role, model.Name, port, err) {
			return
		}
		time.Sleep(restartDelay)
	}
}

func (s *Supervisor) targetForRole(role, requested string) (Target, error) {
	current, err := s.store.Load()
	if err != nil {
		return Target{}, err
	}

	requested = strings.TrimSpace(requested)
	activeName := current.Active.Embedding
	if role == "chat" {
		activeName = current.Active.Chat
	}
	activeName = strings.TrimSpace(activeName)

	targetName := activeName
	if requested != "" {
		targetName = requested
	}
	if targetName == "" {
		return Target{}, fmt.Errorf("no active %s model selected", role)
	}

	if err := validateRoleSelection(s.cfg, s.catalog, role, targetName); err != nil {
		return Target{}, err
	}

	s.mu.RLock()
	proc := s.process[role]
	s.mu.RUnlock()
	if proc == nil || !proc.Ready() || proc.Model().Name != targetName {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := s.switchRole(ctx, role, targetName); err != nil {
			return Target{}, err
		}
		s.mu.RLock()
		proc = s.process[role]
		s.mu.RUnlock()
	}
	if proc == nil || !proc.Ready() {
		return Target{}, fmt.Errorf("%s runtime is not ready", role)
	}
	return Target{
		Model:   proc.Model(),
		BaseURL: proc.BaseURL(),
		Port:    proc.Port(),
	}, nil
}

func (s *Supervisor) recordExit(role string, proc *ManagedProcess, err error) bool {
	s.mu.Lock()
	currentProc := s.process[role]
	if currentProc != nil && currentProc != proc {
		s.mu.Unlock()
		return false
	}
	delete(s.process, role)
	tracker := s.failures[role]
	startedAt := proc.LastStartedAt()
	exitedAt := proc.LastExitedAt()
	rapid := !startedAt.IsZero() && !exitedAt.IsZero() && exitedAt.Sub(startedAt) <= rapidFailureThreshold
	if rapid {
		if tracker.FirstFailure.IsZero() || exitedAt.Sub(tracker.FirstFailure) > rapidFailureWindow {
			tracker = restartTracker{Count: 1, FirstFailure: exitedAt}
		} else {
			tracker.Count++
		}
		if tracker.Count >= rapidFailureLimit {
			tracker.Suppressed = true
			if tracker.SuppressedAt.IsZero() {
				tracker.SuppressedAt = exitedAt
			}
		}
	} else {
		tracker = restartTracker{}
	}
	s.failures[role] = tracker
	s.mu.Unlock()

	lastError := ""
	if err != nil {
		lastError = err.Error()
	}
	if tracker.Suppressed {
		lastError = fmt.Sprintf("restart suppressed after %d rapid failures: %s", tracker.Count, lastError)
	}
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.Role = role
		current.ModelName = proc.Model().Name
		current.Port = proc.Port()
		current.PID = proc.PID()
		current.Running = false
		current.LastExitAt = nowString()
		current.LastError = lastError
		current.RestartCount = tracker.Count
		current.RestartSuppressed = tracker.Suppressed
		if tracker.SuppressedAt.IsZero() {
			current.RestartSuppressedAt = ""
		} else {
			current.RestartSuppressedAt = tracker.SuppressedAt.Format(time.RFC3339Nano)
		}
		st.Processes[role] = current
		return nil
	})
	return tracker.Suppressed
}

func (s *Supervisor) recordRestartFailure(role, modelName string, port int, err error) bool {
	s.mu.Lock()
	tracker := s.failures[role]
	now := time.Now().UTC()
	if tracker.FirstFailure.IsZero() || now.Sub(tracker.FirstFailure) > rapidFailureWindow {
		tracker = restartTracker{Count: 1, FirstFailure: now}
	} else {
		tracker.Count++
	}
	if tracker.Count >= rapidFailureLimit {
		tracker.Suppressed = true
		if tracker.SuppressedAt.IsZero() {
			tracker.SuppressedAt = now
		}
	}
	s.failures[role] = tracker
	delete(s.process, role)
	s.mu.Unlock()

	lastError := err.Error()
	if tracker.Suppressed {
		lastError = fmt.Sprintf("restart suppressed after %d rapid failures: %s", tracker.Count, lastError)
	}
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.Role = role
		current.ModelName = modelName
		current.Port = port
		current.Running = false
		current.LastExitAt = nowString()
		current.LastError = lastError
		current.RestartCount = tracker.Count
		current.RestartSuppressed = tracker.Suppressed
		if tracker.SuppressedAt.IsZero() {
			current.RestartSuppressedAt = ""
		} else {
			current.RestartSuppressedAt = tracker.SuppressedAt.Format(time.RFC3339Nano)
		}
		st.Processes[role] = current
		return nil
	})
	return tracker.Suppressed
}

func (s *Supervisor) recordRoleError(role, modelName string, port int, err error) {
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.Role = role
		current.ModelName = modelName
		current.Port = port
		current.Running = false
		current.LastExitAt = nowString()
		if err != nil {
			current.LastError = err.Error()
		}
		st.Processes[role] = current
		return nil
	})
}

func (s *Supervisor) clearRestartState(role string) {
	s.mu.Lock()
	delete(s.failures, role)
	s.mu.Unlock()
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.RestartCount = 0
		current.RestartSuppressed = false
		current.RestartSuppressedAt = ""
		st.Processes[role] = current
		return nil
	})
}

func (s *Supervisor) isRestartSuppressed(role string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failures[role].Suppressed
}

func (s *Supervisor) currentProcess(role string) *ManagedProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.process[role]
}

func (s *Supervisor) isStopping() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopping
}

func buildRoleHealth(role, activeName string, stored state.ProcessState, proc *ManagedProcess) RoleHealth {
	if proc != nil {
		ready := proc.Running() && checkPort(proc.Port())
		return RoleHealth{
			Role:              role,
			ModelName:         proc.Model().Name,
			Port:              proc.Port(),
			Running:           proc.Running(),
			Ready:             ready,
			LastError:         proc.LastError(),
			RestartCount:      stored.RestartCount,
			RestartSuppressed: stored.RestartSuppressed,
		}
	}

	modelName := activeName
	if modelName == "" {
		modelName = stored.ModelName
	}
	return RoleHealth{
		Role:              role,
		ModelName:         modelName,
		Port:              stored.Port,
		Running:           stored.Running,
		Ready:             false,
		LastError:         stored.LastError,
		RestartCount:      stored.RestartCount,
		RestartSuppressed: stored.RestartSuppressed,
	}
}

func validateRoleSelection(cfg *config.Config, catalog *models.Catalog, role, name string) error {
	model, err := catalog.Require(name)
	if err != nil {
		return err
	}
	if model.Role != role {
		return fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role)
	}
	path := pull.ResolveModelPath(cfg, model)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("model %q is not downloaded at %s; pull it first", model.Name, path)
	}
	return nil
}

func rolePort(role string, model config.ModelConfig) int {
	if model.Port != 0 {
		return model.Port
	}
	if role == "chat" {
		return defaultChatPort
	}
	return defaultEmbeddingPort
}

func checkPort(port int) bool {
	if port == 0 {
		return false
	}
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
