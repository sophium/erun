package orchestration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContributeClonePath returns the canonical clone target for ERun
// contribute mode ($HOME/git/erun), resolved inside the current env —
// the host for local-agent, the pod for remote-agent.
func ContributeClonePath(homeDir string) (string, error) {
	resolved, err := ResolveContributeHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, "git", "erun"), nil
}

func ResolveContributeHomeDir(homeDir string) (string, error) {
	homeDir = strings.TrimSpace(homeDir)
	if homeDir != "" {
		return homeDir, nil
	}
	resolved, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return resolved, nil
}
