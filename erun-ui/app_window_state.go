package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const appWindowStateFileName = "window-state.json"

type appWindowState struct {
	Maximised bool `json:"maximised"`
}

func defaultAppWindowStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", appWindowStateFileName)
}

func loadAppWindowState(path string) appWindowState {
	if path == "" {
		return appWindowState{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return appWindowState{}
	}

	var state appWindowState
	if err := json.Unmarshal(data, &state); err != nil {
		return appWindowState{}
	}
	return state
}

func saveAppWindowState(path string, state appWindowState) error {
	if path == "" {
		return nil
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
