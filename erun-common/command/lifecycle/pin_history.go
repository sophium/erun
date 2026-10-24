package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// pinHistoryFileName records the version before the last re-pin, so a revert is one motion.
const pinHistoryFileName = "pin-history.json"

type pinHistory struct {
	Previous map[string]string `json:"previous"`
}

func pinHistoryPath(projectRoot string) string {
	return filepath.Join(projectRoot, config.ProjectConfigDir, pinHistoryFileName)
}

func pinHistoryKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// RecordPinPrevious records the version before a re-pin; a no-op re-pin keeps the record so
// reverting twice still reaches the original version.
func RecordPinPrevious(projectRoot, tenant, environment, previous string) error {
	previous = strings.TrimSpace(previous)
	if previous == "" {
		return nil
	}
	path := pinHistoryPath(projectRoot)
	history := readPinHistory(path)
	if history.Previous == nil {
		history.Previous = map[string]string{}
	}
	history.Previous[pinHistoryKey(tenant, environment)] = previous
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// PinPrevious reports the version recorded before the last re-pin.
func PinPrevious(projectRoot, tenant, environment string) (string, bool) {
	history := readPinHistory(pinHistoryPath(projectRoot))
	previous, ok := history.Previous[pinHistoryKey(tenant, environment)]
	return strings.TrimSpace(previous), ok && strings.TrimSpace(previous) != ""
}

func readPinHistory(path string) pinHistory {
	data, err := os.ReadFile(path)
	if err != nil {
		return pinHistory{}
	}
	var history pinHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return pinHistory{}
	}
	return history
}
