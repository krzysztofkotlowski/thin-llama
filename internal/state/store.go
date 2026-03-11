package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const FileName = "state.json"

type Store struct {
	path string
	mu   sync.Mutex
}

func New(dir string) *Store {
	return &Store{path: filepath.Join(dir, FileName)}
}

func (s *Store) Path() string {
	return s.path
}

func DefaultState() *State {
	return &State{
		Models:    map[string]ModelState{},
		Processes: map[string]ProcessState{},
		Downloads: map[string]DownloadStatus{},
	}
}

func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked(st)
}

func (s *Store) Update(fn func(*State) error) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadUnlocked()
	if err != nil {
		return nil, err
	}
	if err := fn(current); err != nil {
		return nil, err
	}
	if err := s.saveUnlocked(current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) loadUnlocked() (*State, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultState(), nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	current := DefaultState()
	if err := json.Unmarshal(data, current); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if current.Models == nil {
		current.Models = map[string]ModelState{}
	}
	if current.Processes == nil {
		current.Processes = map[string]ProcessState{}
	}
	if current.Downloads == nil {
		current.Downloads = map[string]DownloadStatus{}
	}
	return current, nil
}

func (s *Store) saveUnlocked(st *State) error {
	if st == nil {
		st = DefaultState()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
