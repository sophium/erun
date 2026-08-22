package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteWorkingTreeFileParams names the write explicitly: Path is never
// defaulted, so content can never land somewhere the caller did not choose.
type WriteWorkingTreeFileParams struct {
	// Path is the destination, absolute or relative to the working tree root.
	Path string
	// Content is written verbatim. It is data, not a shell fragment: nothing
	// in it is interpreted, expanded, or executed.
	Content string
}

// WriteWorkingTreeFileResult is what the write actually did.
type WriteWorkingTreeFileResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// WriteWorkingTreeFile writes Content to Path inside root byte-for-byte — no
// heredoc, no generated script, no shell anywhere in the path. The resolved
// path is traced and the write is refused outright when it would land outside
// root, so granting this operation can never become a path traversal into the
// rest of the pod.
func WriteWorkingTreeFile(ctx Context, root string, params WriteWorkingTreeFileParams) (WriteWorkingTreeFileResult, error) {
	resolved, err := resolveWorkingTreePath(root, params.Path)
	if err != nil {
		return WriteWorkingTreeFileResult{}, err
	}

	content := []byte(params.Content)
	ctx.TraceCommand("", "write-file", resolved)
	if ctx.DryRun {
		return WriteWorkingTreeFileResult{Path: resolved, Bytes: int64(len(content))}, nil
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return WriteWorkingTreeFileResult{}, fmt.Errorf("write %q: %w", resolved, err)
	}
	if err := os.WriteFile(resolved, content, 0o644); err != nil {
		return WriteWorkingTreeFileResult{}, fmt.Errorf("write %q: %w", resolved, err)
	}
	return WriteWorkingTreeFileResult{Path: resolved, Bytes: int64(len(content))}, nil
}

// resolveWorkingTreePath validates and resolves requested against root,
// refusing anything that would land outside it — a "../" escape, an absolute
// path elsewhere, or root itself (a directory, not a file to write).
func resolveWorkingTreePath(root, requested string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("path is required")
	}

	resolved := filepath.Clean(requested)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Clean(filepath.Join(root, resolved))
	}

	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the working tree %q", requested, root)
	}
	return resolved, nil
}
