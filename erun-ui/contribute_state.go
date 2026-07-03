package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const contributeStateFileName = "contribute-state.json"

// contributeState persists the per-env contribute toggle as a desktop-UI
// preference, kept out of env config.yaml because it is not env semantics.
type contributeState struct {
	Flags map[string]bool `json:"flags,omitempty"`
}

// contributeStore serializes mutations so a toggle click never races with the
// initial disk load.
type contributeStore struct {
	path string
	mu   sync.Mutex
	data contributeState
}

func defaultContributeStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", contributeStateFileName)
}

func newContributeStore(path string) *contributeStore {
	store := &contributeStore{path: path, data: contributeState{Flags: map[string]bool{}}}
	store.loadFromDisk()
	return store
}

func (s *contributeStore) loadFromDisk() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var state contributeState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Flags == nil {
		state.Flags = map[string]bool{}
	}
	s.mu.Lock()
	s.data = state
	s.mu.Unlock()
}

func (s *contributeStore) saveToDisk() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	snapshot := contributeState{Flags: cloneContributeFlags(s.data.Flags)}
	s.mu.Unlock()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *contributeStore) get(selection uiSelection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Flags[contributeFlagKey(selection)]
}

func (s *contributeStore) set(selection uiSelection, on bool) {
	key := contributeFlagKey(selection)
	s.mu.Lock()
	if s.data.Flags == nil {
		s.data.Flags = map[string]bool{}
	}
	if on {
		s.data.Flags[key] = true
	} else {
		delete(s.data.Flags, key)
	}
	s.mu.Unlock()
}

func cloneContributeFlags(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		if v {
			out[k] = true
		}
	}
	return out
}

func contributeFlagKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "/" + selection.Environment
}
