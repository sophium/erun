package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// WriteWorkingTreeFileParams has no default Path, so content lands only where the caller chose.
type WriteWorkingTreeFileParams struct {
	// Path is absolute or relative to the working tree root.
	Path string
	// Content is written verbatim; nothing in it is interpreted.
	Content string
}

// WriteWorkingTreeFileResult is what the write actually did.
type WriteWorkingTreeFileResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// WriteWorkingTreeFile writes Content without any shell and refuses paths outside root.
func WriteWorkingTreeFile(ctx core.Context, root string, params WriteWorkingTreeFileParams) (WriteWorkingTreeFileResult, error) {
	resolved, _, err := resolveWorkingTreePath(root, params.Path)
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

// resolveWorkingTreePath refuses "../" escapes, outside absolute paths, root itself, and
// any symlink between the real root and the target. Symlinks are refused rather than
// followed to avoid a TOCTOU window. The second result is the real root; callers must use it.
func resolveWorkingTreePath(root, requested string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", "", fmt.Errorf("path is required")
	}

	realRoot, err := realWorkingTreeRoot(root)
	if err != nil {
		return "", "", err
	}

	resolved := filepath.Clean(requested)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Clean(filepath.Join(realRoot, resolved))
	}

	relative, err := filepath.Rel(realRoot, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q is outside the working tree %q", requested, root)
	}

	if err := refuseSymlinkInPath(realRoot, relative, requested); err != nil {
		return "", "", err
	}

	return resolved, realRoot, nil
}

// realWorkingTreeRoot resolves root's symlinks so containment uses the real location.
func realWorkingTreeRoot(root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve working tree root %q: %w", root, err)
	}
	return filepath.Clean(resolved), nil
}

// refuseSymlinkInPath stops at the first missing component: nothing beneath it can exist.
func refuseSymlinkInPath(root, relative, requested string) error {
	current := root
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q traverses %q, which is a symlink; writing through a symlink is refused", requested, current)
		}
	}
	return nil
}
