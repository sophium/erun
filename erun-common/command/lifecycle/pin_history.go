package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// pinHistoryFileName records what the repo was pinned to before the last
// re-pin, so reverting is one motion rather than remembering a number. It lives
// beside the project's erun config because it describes this tree.
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

// RecordPinPrevious remembers the version this environment was on before a
// re-pin. A re-pin that changes nothing does not overwrite the record: reverting
// twice must still reach the version you came from, not the one you are on.
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

// PinPrevious reports the version recorded before the last re-pin, which is what
// a revert targets.
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
