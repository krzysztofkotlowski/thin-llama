package runtime

import "testing"

func TestResolvePortsUsesDefaults(t *testing.T) {
	chat, embedding, err := ResolvePorts(0, 0)
	if err != nil {
		t.Fatalf("ResolvePorts() unexpected error: %v", err)
	}
	if chat != defaultChatPort || embedding != defaultEmbeddingPort {
		t.Fatalf("ResolvePorts() = (%d, %d)", chat, embedding)
	}
}

func TestResolvePortsRejectsDuplicatePorts(t *testing.T) {
	if _, _, err := ResolvePorts(1234, 1234); err == nil {
		t.Fatal("ResolvePorts() expected duplicate port error")
	}
}
