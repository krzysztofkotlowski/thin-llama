package state

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	store := New(t.TempDir())
	original := &State{
		Active: ActiveState{
			Chat:      "chat-model",
			Embedding: "embed-model",
		},
		Models: map[string]ModelState{
			"chat-model": {Name: "chat-model", Available: true, LocalPath: "/models/chat.gguf"},
		},
		Processes: map[string]ProcessState{
			"chat": {Role: "chat", ModelName: "chat-model", Port: 11435, Running: true},
		},
		Downloads: map[string]DownloadStatus{
			"chat-model": {ModelName: "chat-model", Status: "available"},
		},
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if loaded.Active.Chat != "chat-model" {
		t.Fatalf("Load().Active.Chat = %q", loaded.Active.Chat)
	}
	if loaded.Processes["chat"].Port != 11435 {
		t.Fatalf("Load().Processes[chat].Port = %d", loaded.Processes["chat"].Port)
	}
	if filepath.Base(store.Path()) != FileName {
		t.Fatalf("Path() = %q", store.Path())
	}
}

func TestStoreUpdateInitializesDefaultState(t *testing.T) {
	store := New(t.TempDir())

	updated, err := store.Update(func(st *State) error {
		st.Active.Chat = "chat-model"
		return nil
	})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if updated.Active.Chat != "chat-model" {
		t.Fatalf("Update().Active.Chat = %q", updated.Active.Chat)
	}
}
