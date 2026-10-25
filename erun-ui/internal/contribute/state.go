package contribute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const stateFileName = "contribute-state.json"

// persistedState is a desktop-UI preference, kept out of env config.yaml.
type persistedState struct {
	Flags map[string]bool `json:"flags,omitempty"`
}

// flagStore serializes mutations so a toggle cannot race the initial load.
type flagStore struct {
	path string
	mu   sync.Mutex
	data persistedState
}

// DefaultStatePath is where the desktop keeps the contribute flags.
func DefaultStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", stateFileName)
}

func newFlagStore(path string) *flagStore {
	store := &flagStore{path: path, data: persistedState{Flags: map[string]bool{}}}
	store.loadFromDisk()
	return store
}

func (s *flagStore) loadFromDisk() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var state persistedState
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

func (s *flagStore) saveToDisk() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	snapshot := persistedState{Flags: cloneFlags(s.data.Flags)}
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

func (s *flagStore) get(selection uimodel.Selection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Flags[flagKey(selection)]
}

func (s *flagStore) set(selection uimodel.Selection, on bool) {
	key := flagKey(selection)
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

func cloneFlags(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		if v {
			out[k] = true
		}
	}
	return out
}

func flagKey(selection uimodel.Selection) string {
	selection = uimodel.NormalizeSelection(selection)
	return selection.Tenant + "/" + selection.Environment
}
