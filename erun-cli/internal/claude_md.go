package internal

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	claudeMDOpenMarker  = "<!-- erun-agents-md-hook -->"
	claudeMDCloseMarker = "<!-- /erun-agents-md-hook -->"
)

// claudeMDBlock is erun's managed agent-instructions region for the global
// CLAUDE.md / Codex instructions file. It is delimited by the open/close markers
// so it can be rewritten in place on every run: editing this constant updates
// existing files the next time the CLI runs, not only freshly created ones. Keep
// it byte-aligned with the in-pod copy in
// erun-devops/docker/erun-devops/entrypoint.sh.
const claudeMDBlock = claudeMDOpenMarker + "\n" +
	"# Agent Instructions\n" +
	"\n" +
	"IMPORTANT: Before doing anything else, read `AGENTS.md` in the project root. This is mandatory — do not skip it.\n" +
	"Also read `AGENTS.md` in any subdirectory relevant to the task at hand,\n" +
	"as subdirectories may contain more specific guidance.\n" +
	"\n" +
	"When the project's structure is explicit — AGENTS.md, documented module boundaries, a named file or function — read the source directly instead of spawning sub-agent searches to rediscover it. Answer the operator's questions before acting; a question is not authorization to begin the work it hints at.\n" +
	claudeMDCloseMarker + "\n"

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
	updated := upsertAgentInstructionsBlock(string(data))
	if updated == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o600)
}

// upsertAgentInstructionsBlock returns content with erun's managed
// agent-instructions block present and current. An existing block — delimited by
// the open/close markers — is replaced in place; otherwise the block is appended,
// separated from any preceding content by one blank line. The result is stable:
// applying the function to its own output returns it unchanged.
func upsertAgentInstructionsBlock(content string) string {
	body := strings.TrimRight(stripAgentInstructionsBlock(content), "\n")
	if body == "" {
		return claudeMDBlock
	}
	return body + "\n\n" + claudeMDBlock
}

// stripAgentInstructionsBlock removes erun's managed block (markers inclusive)
// from content, leaving everything outside it untouched. A dangling open marker
// with no matching close drops everything from the open marker onward so the
// caller can re-append a clean block.
func stripAgentInstructionsBlock(content string) string {
	open := strings.Index(content, claudeMDOpenMarker)
	if open == -1 {
		return content
	}
	afterOpen := open + len(claudeMDOpenMarker)
	closeRel := strings.Index(content[afterOpen:], claudeMDCloseMarker)
	if closeRel == -1 {
		return content[:open]
	}
	end := afterOpen + closeRel + len(claudeMDCloseMarker)
	return content[:open] + content[end:]
}
