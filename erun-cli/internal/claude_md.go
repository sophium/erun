package internal

import (
	"os"
	"path/filepath"
	"strings"
)

const claudeMDMarker = "erun-agents-md-hook"

const claudeMDBlock = "\n<!-- erun-agents-md-hook -->\n# Agent Instructions\n\nIMPORTANT: Before doing anything else, read `AGENTS.md` in the project root. This is mandatory — do not skip it.\nAlso read `AGENTS.md` in any subdirectory relevant to the task at hand,\nas subdirectories may contain more specific guidance.\n<!-- /erun-agents-md-hook -->\n"

func EnsureGlobalAgentInstructions() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := ensureGlobalClaudeMDWithHomeDir(homeDir); err != nil {
		return err
	}
	return ensureGlobalCodexInstructionsWithHomeDir(homeDir)
}

func ensureGlobalClaudeMDWithHomeDir(homeDir string) error {
	return ensureAgentInstructionsFile(filepath.Join(homeDir, ".claude"), "CLAUDE.md")
}

func ensureGlobalCodexInstructionsWithHomeDir(homeDir string) error {
	return ensureAgentInstructionsFile(filepath.Join(homeDir, ".codex"), "instructions.md")
}

func ensureAgentInstructionsFile(dir, filename string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), claudeMDMarker) {
		return nil
	}
	content := string(data)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += claudeMDBlock
	return os.WriteFile(path, []byte(content), 0o600)
}
