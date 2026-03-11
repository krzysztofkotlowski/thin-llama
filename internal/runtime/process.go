package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
)

type ManagedProcess struct {
	role           string
	model          config.ModelConfig
	modelPath      string
	binary         string
	port           int
	startupTimeout time.Duration

	mu        sync.RWMutex
	cmd       *exec.Cmd
	done      chan error
	lastErr   string
	lastStart time.Time
	lastExit  time.Time
	ready     bool
	stopping  bool
}

func NewManagedProcess(role string, model config.ModelConfig, modelPath, binary string, port int, startupTimeout time.Duration) *ManagedProcess {
	if startupTimeout <= 0 {
		startupTimeout = 60 * time.Second
	}
	return &ManagedProcess{
		role:           role,
		model:          model,
		modelPath:      modelPath,
		binary:         binary,
		port:           port,
		startupTimeout: startupTimeout,
	}
}

func (p *ManagedProcess) Start(ctx context.Context) error {
	args := []string{"-m", p.modelPath, "--port", strconv.Itoa(p.port)}
	if p.model.ContextSize > 0 {
		args = append(args, "-c", strconv.Itoa(p.model.ContextSize))
	}
	if p.model.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(p.model.Threads))
	}
	if p.model.GPULayers > 0 {
		args = append(args, "-ngl", strconv.Itoa(p.model.GPULayers))
	}
	if p.role == "embedding" {
		args = append(args, "--embedding")
	}
	args = append(args, p.model.ExtraArgs...)

	cmd := exec.CommandContext(ctx, p.binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s model %s: %w", p.role, p.model.Name, err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.done = nil
	p.lastStart = time.Now().UTC()
	p.ready = false
	p.stopping = false
	p.lastErr = ""
	p.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()

	waitCtx, cancel := context.WithTimeout(ctx, p.startupTimeout)
	defer cancel()
	if err := waitForTCP(waitCtx, p.port, done); err != nil {
		_ = terminateProcess(cmd, done, 2*time.Second)
		return fmt.Errorf("wait for %s model %s on port %d: %w", p.role, p.model.Name, p.port, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = done
	p.ready = true
	p.stopping = false
	p.lastErr = ""
	return nil
}

func (p *ManagedProcess) Stop(ctx context.Context) error {
	p.mu.Lock()
	if p.cmd == nil || p.cmd.Process == nil {
		p.mu.Unlock()
		return nil
	}
	cmd := p.cmd
	done := p.done
	p.stopping = true
	p.ready = false
	p.mu.Unlock()

	if err := terminateProcess(cmd, done, 5*time.Second); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	return nil
}

func (p *ManagedProcess) WaitChan() <-chan error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.done
}

func (p *ManagedProcess) PID() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *ManagedProcess) Running() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cmd != nil && p.cmd.Process != nil && p.cmd.ProcessState == nil
}

func (p *ManagedProcess) Ready() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ready
}

func (p *ManagedProcess) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.port)
}

func (p *ManagedProcess) Port() int {
	return p.port
}

func (p *ManagedProcess) Model() config.ModelConfig {
	return p.model
}

func (p *ManagedProcess) Role() string {
	return p.role
}

func (p *ManagedProcess) MarkExited(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ready = false
	p.lastExit = time.Now().UTC()
	if err != nil {
		p.lastErr = err.Error()
	}
}

func (p *ManagedProcess) LastError() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastErr
}

func (p *ManagedProcess) LastStartedAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastStart
}

func (p *ManagedProcess) LastExitedAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastExit
}

func (p *ManagedProcess) WasStopped() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stopping
}

func terminateProcess(cmd *exec.Cmd, done <-chan error, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil && err.Error() != "os: process already finished" {
			return err
		}
		if done != nil {
			<-done
		}
		return nil
	}
}

func waitForTCP(ctx context.Context, port int, done <-chan error) error {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if done != nil {
			select {
			case err, ok := <-done:
				if !ok || err == nil {
					return fmt.Errorf("process exited before listening")
				}
				return err
			default:
			}
		}
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
