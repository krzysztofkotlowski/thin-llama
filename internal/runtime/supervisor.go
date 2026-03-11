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
	PID               int    `json:"pid,omitempty"`
	Running           bool   `json:"running"`
	Ready             bool   `json:"ready"`
	LastError         string `json:"last_error,omitempty"`
	StatusMessage     string `json:"status_message,omitempty"`
	OrphanDetected    bool   `json:"orphan_detected,omitempty"`
	RestartCount      int    `json:"restart_count,omitempty"`
	RestartSuppressed bool   `json:"restart_suppressed,omitempty"`
}

type HealthSnapshot struct {
	OK           bool       `json:"ok"`
	RuntimeReady bool       `json:"runtime_ready"`
	Chat         RoleHealth `json:"chat"`
	Embedding    RoleHealth `json:"embedding"`
}

type RuntimeSnapshot struct {
	OK           bool              `json:"ok"`
	RuntimeReady bool              `json:"runtime_ready"`
	Active       state.ActiveState `json:"active"`
	Chat         RoleHealth        `json:"chat"`
	Embedding    RoleHealth        `json:"embedding"`
}

type restartTracker struct {
	ModelName    string
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
	roleMu   map[string]*sync.Mutex
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
		roleMu: map[string]*sync.Mutex{
			"chat":      &sync.Mutex{},
			"embedding": &sync.Mutex{},
		},
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

func (s *Supervisor) Snapshot() RuntimeSnapshot {
	current, err := s.store.Load()
	if err != nil {
		return RuntimeSnapshot{
			OK:     true,
			Active: state.ActiveState{},
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
	return RuntimeSnapshot{
		OK:           true,
		RuntimeReady: chat.Ready && embedding.Ready,
		Active:       current.Active,
		Chat:         chat,
		Embedding:    embedding,
	}
}

func (s *Supervisor) Health() HealthSnapshot {
	snapshot := s.Snapshot()
	return HealthSnapshot{
		OK:           snapshot.OK,
		RuntimeReady: snapshot.RuntimeReady,
		Chat:         snapshot.Chat,
		Embedding:    snapshot.Embedding,
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
		if _, err := s.ensureRoleReady(ctx, "chat", chatName, true); err != nil {
			return err
		}
	}
	if embeddingName != "" {
		if _, err := s.ensureRoleReady(ctx, "embedding", embeddingName, true); err != nil {
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
	_, _ = s.ensureRoleReady(ctx, role, model.Name, false)
}

func (s *Supervisor) ensureRoleReady(ctx context.Context, role, name string, persistActive bool) (*ManagedProcess, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("no %s model selected", role)
	}

	model, err := s.catalog.Require(name)
	if err != nil {
		return nil, err
	}
	if model.Role != role {
		return nil, fmt.Errorf("model %q is role %q, expected %q", model.Name, model.Role, role)
	}
	modelPath := pull.ResolveModelPath(s.cfg, model)
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("model %q is not downloaded at %s; pull it first", model.Name, modelPath)
	}

	lock := s.roleLock(role)
	lock.Lock()
	defer lock.Unlock()

	current, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	activeName := activeModelForRole(current.Active, role)
	if tracker := s.failureTracker(role); tracker.Suppressed && tracker.ModelName == model.Name {
		lastError := current.Processes[role].LastError
		if lastError == "" {
			lastError = fmt.Sprintf("%s model %q is restart suppressed", role, model.Name)
		}
		return nil, fmt.Errorf("%s", lastError)
	}

	oldProc := s.currentProcess(role)
	if oldProc != nil && oldProc.Model().Name == model.Name && oldProc.Ready() && oldProc.Running() && checkPort(oldProc.Port()) {
		if persistActive && activeName != model.Name {
			if err := s.persistActiveModel(role, model.Name); err != nil {
				return nil, err
			}
		}
		return oldProc, nil
	}

	if oldProc != nil {
		if err := oldProc.Stop(ctx); err != nil {
			return nil, err
		}
		s.deleteProcess(role, oldProc)
	}

	port := rolePort(role, model)
	if checkPort(port) {
		err := orphanError(role, model.Name, port)
		s.recordRuntimeIssue(role, model.Name, port, 0, err.Error(), true)
		return nil, err
	}

	proc, err := s.startRoleLocked(ctx, role, model, modelPath, port)
	if err != nil {
		s.recordRoleError(role, model.Name, port, err)
		return nil, err
	}
	if persistActive && activeName != model.Name {
		if err := s.persistActiveModel(role, model.Name); err != nil {
			return nil, err
		}
	}
	return proc, nil
}

func (s *Supervisor) startRoleLocked(ctx context.Context, role string, model config.ModelConfig, modelPath string, port int) (*ManagedProcess, error) {
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("%s model %q is not available at %s: %w", role, model.Name, modelPath, err)
	}

	proc := NewManagedProcess(role, model, modelPath, s.cfg.LlamaServerBin, port, s.cfg.StartupTimeout())
	if err := proc.Start(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.process[role] = proc
	s.failures[role] = restartTracker{ModelName: model.Name}
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
			StatusMessage:       "",
			OrphanDetected:      false,
			RestartCount:        0,
			RestartSuppressed:   false,
			RestartSuppressedAt: "",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.watchProcess(role, proc)
	return proc, nil
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
		if s.isStopping() || s.isRestartSuppressed(role, model.Name) {
			return
		}
		activeName, err := s.activeModelName(role)
		if err != nil || activeName != model.Name {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.StartupTimeout())
		err = s.restartRole(ctx, role, model, port)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(restartDelay)
	}
}

func (s *Supervisor) restartRole(ctx context.Context, role string, model config.ModelConfig, port int) error {
	lock := s.roleLock(role)
	lock.Lock()
	defer lock.Unlock()

	if tracker := s.failureTracker(role); tracker.Suppressed && tracker.ModelName == model.Name {
		current, _ := s.store.Load()
		if current != nil {
			if lastError := current.Processes[role].LastError; lastError != "" {
				return fmt.Errorf("%s", lastError)
			}
		}
		return fmt.Errorf("%s model %q is restart suppressed", role, model.Name)
	}

	activeName, err := s.activeModelName(role)
	if err != nil || activeName != model.Name {
		return nil
	}

	oldProc := s.currentProcess(role)
	if oldProc != nil && oldProc.Model().Name == model.Name && oldProc.Ready() && oldProc.Running() && checkPort(oldProc.Port()) {
		return nil
	}
	if oldProc != nil {
		if err := oldProc.Stop(ctx); err != nil {
			return err
		}
		s.deleteProcess(role, oldProc)
	}

	if checkPort(port) {
		err := orphanError(role, model.Name, port)
		s.recordRestartFailure(role, model.Name, port, err)
		s.recordRuntimeIssue(role, model.Name, port, 0, err.Error(), true)
		return err
	}

	modelPath := pull.ResolveModelPath(s.cfg, model)
	if _, err := os.Stat(modelPath); err != nil {
		err = fmt.Errorf("%s model %q is not available at %s: %w", role, model.Name, modelPath, err)
		s.recordRestartFailure(role, model.Name, port, err)
		return err
	}
	if _, err := s.startRoleLocked(ctx, role, model, modelPath, port); err != nil {
		s.recordRestartFailure(role, model.Name, port, err)
		return err
	}
	return nil
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

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.StartupTimeout())
	defer cancel()
	proc, err := s.ensureRoleReady(ctx, role, targetName, requested != "")
	if err != nil {
		return Target{}, err
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
		if tracker.ModelName != proc.Model().Name || tracker.FirstFailure.IsZero() || exitedAt.Sub(tracker.FirstFailure) > rapidFailureWindow {
			tracker = restartTracker{ModelName: proc.Model().Name, Count: 1, FirstFailure: exitedAt}
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
		tracker = restartTracker{ModelName: proc.Model().Name}
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
		current.StatusMessage = ""
		current.OrphanDetected = false
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
	if tracker.ModelName != modelName || tracker.FirstFailure.IsZero() || now.Sub(tracker.FirstFailure) > rapidFailureWindow {
		tracker = restartTracker{ModelName: modelName, Count: 1, FirstFailure: now}
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
		current.PID = 0
		current.Running = false
		current.LastExitAt = nowString()
		current.LastError = lastError
		current.StatusMessage = ""
		current.OrphanDetected = false
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
		current.PID = 0
		current.Running = false
		current.LastExitAt = nowString()
		if err != nil {
			current.LastError = err.Error()
		}
		current.StatusMessage = ""
		current.OrphanDetected = false
		st.Processes[role] = current
		return nil
	})
}

func (s *Supervisor) recordRuntimeIssue(role, modelName string, port int, pid int, message string, orphanDetected bool) {
	_, _ = s.store.Update(func(st *state.State) error {
		current := st.Processes[role]
		current.Role = role
		current.ModelName = modelName
		current.Port = port
		current.PID = pid
		current.Running = false
		current.LastExitAt = nowString()
		current.LastError = message
		current.StatusMessage = message
		current.OrphanDetected = orphanDetected
		st.Processes[role] = current
		return nil
	})
}

func (s *Supervisor) persistActiveModel(role, modelName string) error {
	_, err := s.store.Update(func(st *state.State) error {
		if role == "chat" {
			st.Active.Chat = modelName
		} else {
			st.Active.Embedding = modelName
		}
		return nil
	})
	return err
}

func (s *Supervisor) roleLock(role string) *sync.Mutex {
	if lock, ok := s.roleMu[role]; ok {
		return lock
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lock, ok := s.roleMu[role]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	s.roleMu[role] = lock
	return lock
}

func (s *Supervisor) deleteProcess(role string, proc *ManagedProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.process[role]
	if proc == nil || current == nil || current == proc {
		delete(s.process, role)
	}
}

func (s *Supervisor) activeModelName(role string) (string, error) {
	current, err := s.store.Load()
	if err != nil {
		return "", err
	}
	return activeModelForRole(current.Active, role), nil
}

func (s *Supervisor) failureTracker(role string) restartTracker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failures[role]
}

func (s *Supervisor) isRestartSuppressed(role, modelName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tracker := s.failures[role]
	return tracker.Suppressed && tracker.ModelName == modelName
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
		lastError := proc.LastError()
		if lastError == "" {
			lastError = stored.LastError
		}
		return RoleHealth{
			Role:              role,
			ModelName:         proc.Model().Name,
			Port:              proc.Port(),
			PID:               proc.PID(),
			Running:           proc.Running(),
			Ready:             ready,
			LastError:         lastError,
			StatusMessage:     stored.StatusMessage,
			OrphanDetected:    stored.OrphanDetected,
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
		PID:               stored.PID,
		Running:           stored.Running,
		Ready:             false,
		LastError:         stored.LastError,
		StatusMessage:     stored.StatusMessage,
		OrphanDetected:    stored.OrphanDetected,
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

func activeModelForRole(active state.ActiveState, role string) string {
	if role == "chat" {
		return strings.TrimSpace(active.Chat)
	}
	return strings.TrimSpace(active.Embedding)
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

func orphanError(role, modelName string, port int) error {
	return fmt.Errorf("unmanaged/orphaned process detected for %s model %q on port %d; restart thin-llama to recover", role, modelName, port)
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
