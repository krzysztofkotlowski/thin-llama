package runtime

import (
	"testing"
	"time"

	"github.com/krzysztofkotlowski/thin-llama/internal/config"
	"github.com/krzysztofkotlowski/thin-llama/internal/state"
)

func TestRecordRestartFailureSuppressesAfterRepeatedRapidFailures(t *testing.T) {
	dir := t.TempDir()
	supervisor := NewSupervisor(&config.Config{StateDir: dir}, nil, state.New(dir))

	for i := 0; i < rapidFailureLimit; i++ {
		suppressed := supervisor.recordRestartFailure("chat", "qwen2.5:3b", 11435, assertErr("signal: killed"))
		if i < rapidFailureLimit-1 && suppressed {
			t.Fatalf("unexpected suppression on attempt %d", i+1)
		}
		if i == rapidFailureLimit-1 && !suppressed {
			t.Fatal("expected suppression after repeated rapid failures")
		}
	}

	current, err := supervisor.store.Load()
	if err != nil {
		t.Fatalf("store.Load() unexpected error: %v", err)
	}
	process := current.Processes["chat"]
	if !process.RestartSuppressed {
		t.Fatalf("expected restart suppression in process state: %+v", process)
	}
	if process.RestartCount != rapidFailureLimit {
		t.Fatalf("unexpected restart count: %+v", process)
	}
	if process.RestartSuppressedAt == "" {
		t.Fatalf("expected restart suppression timestamp: %+v", process)
	}
}

func TestRecordExitSuppressesRapidCrashLoop(t *testing.T) {
	dir := t.TempDir()
	supervisor := NewSupervisor(&config.Config{StateDir: dir}, nil, state.New(dir))
	startedAt := time.Now().UTC()
	proc := &ManagedProcess{
		role:      "chat",
		model:     config.ModelConfig{Name: "qwen2.5:3b", Role: "chat"},
		port:      11435,
		lastStart: startedAt,
		lastExit:  startedAt.Add(5 * time.Second),
	}

	supervisor.mu.Lock()
	supervisor.process["chat"] = proc
	supervisor.failures["chat"] = restartTracker{ModelName: "qwen2.5:3b", Count: rapidFailureLimit - 1, FirstFailure: startedAt}
	supervisor.mu.Unlock()

	if !supervisor.recordExit("chat", proc, assertErr("signal: killed")) {
		t.Fatal("expected suppression after rapid crash loop")
	}
}

type testErr string

func (e testErr) Error() string {
	return string(e)
}

func assertErr(message string) error {
	return testErr(message)
}
