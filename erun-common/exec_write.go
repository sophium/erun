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

// resolveWorkingTreePath validates and resolves requested against root,
// refusing anything that would land outside it — a "../" escape, an absolute
// path elsewhere, root itself (a directory, not a file to write), or a path
// that traverses a symlink anywhere between root and the target.
//
// Containment is decided against the real filesystem, not the lexical
// string: root is resolved to its real path first (root itself may be a
// symlink), and every existing path component between the real root and the
// target is checked with Lstat so a symlink planted inside the tree cannot
// redirect the write elsewhere. Writing through an in-tree symlink is
// refused outright rather than followed: the property this command exists to
// guarantee — a write can only land inside the working tree — has to hold
// even when the tree's own agent planted the symlink, and "follow it if the
// target still resolves in-tree" would leave a TOCTOU window between the
// check and the write. The second return value is the real, symlink-free
// root; callers that compute further paths relative to root must use it
// instead of the caller-supplied root string, which may still be a symlink.
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

// realWorkingTreeRoot resolves root to its real, symlink-free path so
// containment is decided against the actual filesystem location rather than
// a symlink that happens to be named root.
func realWorkingTreeRoot(root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve working tree root %q: %w", root, err)
	}
	return filepath.Clean(real), nil
}

// refuseSymlinkInPath walks relative one path component at a time from root,
// refusing as soon as an existing component is a symlink. A component that
// does not exist yet ends the walk: nothing can exist beneath a path that
// does not exist, and the eventual write only ever creates plain files and
// directories.
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
