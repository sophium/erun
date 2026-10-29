package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

var (
	IdeUserHomeDir = os.UserHomeDir
	IdeGlob        = filepath.Glob
	IdeStat        = os.Stat
)

func ResolveIntelliJOptionsDir(hostOS model.HostOS) (string, error) {
	baseDir, err := intelliJOptionsBaseDir(hostOS)
	if err != nil {
		return "", err
	}
	candidates, err := intelliJOptionsCandidates(baseDir)
	if err != nil {
		return "", err
	}
	slices.SortFunc(candidates, func(a, b intelliJOptionsCandidate) int {
		if a.modTime.Equal(b.modTime) {
			return strings.Compare(b.path, a.path)
		}
		if a.modTime.After(b.modTime) {
			return -1
		}
		return 1
	})
	return candidates[0].path, nil
}

type intelliJOptionsCandidate struct {
	path    string
	modTime time.Time
}

func intelliJOptionsBaseDir(hostOS model.HostOS) (string, error) {
	homeDir, err := IdeUserHomeDir()
	if err != nil {
		return "", err
	}
	switch hostOS {
	case model.HostOSDarwin:
		return filepath.Join(homeDir, "Library", "Application Support", "JetBrains"), nil
	case model.HostOSLinux:
		return filepath.Join(homeDir, ".config", "JetBrains"), nil
	case model.HostOSWindows:
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "JetBrains"), nil
		}
		return "", fmt.Errorf("JetBrains options directory base is not configured")
	case model.HostOSUnknown:
	}
	return "", fmt.Errorf("unsupported host OS: %s", hostOS)
}

func intelliJOptionsCandidates(baseDir string) ([]intelliJOptionsCandidate, error) {
	matches, err := IdeGlob(filepath.Join(baseDir, "IntelliJIdea*", "options"))
	if err != nil {
		return nil, err
	}
	candidates := make([]intelliJOptionsCandidate, 0, len(matches))
	for _, match := range matches {
		info, err := IdeStat(match)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		candidates = append(candidates, intelliJOptionsCandidate{path: match, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("IntelliJ IDEA options directory not found under %s", baseDir)
	}
	return candidates, nil
}
