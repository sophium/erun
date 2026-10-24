package eruncommon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
)

// worktreeAdoptStagingName is where a partial copy lands inside the claim, so
// an interrupted adoption never presents itself as a populated claim on the
// next boot.
const worktreeAdoptStagingName = ".erun-worktree-adopt-partial"

// worktreeAdoptPreservedSuffix names the sibling the original tree is set
// aside at once its copy is proven; deploy's relocation notice names the same
// path (announceWorktreeVolumeChange).
const worktreeAdoptPreservedSuffix = ".pre-worktree-volume"

// WorktreeAdoptOutcome says what one adoption run decided.
type WorktreeAdoptOutcome string

const (
	WorktreeAdoptNoClaim         WorktreeAdoptOutcome = "no-claim"
	WorktreeAdoptClaimPopulated  WorktreeAdoptOutcome = "claim-populated"
	WorktreeAdoptNoLegacyTree    WorktreeAdoptOutcome = "no-legacy-tree"
	WorktreeAdoptNotARepository  WorktreeAdoptOutcome = "not-a-repository"
	WorktreeAdoptAdopted         WorktreeAdoptOutcome = "adopted"
	WorktreeAdoptAdoptedOriginal WorktreeAdoptOutcome = "adopted-original-kept"
)

// WorktreeAdoptParams names the two staging paths the runtime chart's init
// container mounts outside /home/erun, so the claim cannot shadow the tree
// being read.
type WorktreeAdoptParams struct {
	LegacyDir string
	ClaimDir  string
}

// WorktreeAdoptResult is the structured result of AdoptWorktree.
type WorktreeAdoptResult struct {
	Outcome      WorktreeAdoptOutcome `json:"outcome"`
	LegacyDir    string               `json:"legacyDir"`
	ClaimDir     string               `json:"claimDir"`
	PreservedDir string               `json:"preservedDir,omitempty"`
	DryRun       bool                 `json:"dryRun,omitempty"`
}

// AdoptWorktree moves an environment's existing repository onto the dedicated
// worktree volume before the pod mounts that volume over it.
//
// An environment created before the worktree claim existed kept its checkout
// on the home volume, at exactly the path the claim now mounts at. Mounting the
// new empty claim there masks the checkout, and the one-way pod->host workspace
// sync then propagates the empty tree over the operator's mirror. Adopting the
// tree into the claim first is what makes the volume change non-destructive.
//
// Adoption is copy-then-set-aside, never move-first: a failed run leaves the
// legacy tree intact, and the claim only counts as adopted once the copy landed
// a real repository. Every later boot finds a populated claim and does nothing.
func AdoptWorktree(ctx core.Context, params WorktreeAdoptParams) (WorktreeAdoptResult, error) {
	result := WorktreeAdoptResult{LegacyDir: params.LegacyDir, ClaimDir: params.ClaimDir, DryRun: ctx.DryRun}
	if params.LegacyDir == "" || params.ClaimDir == "" {
		return result, errors.New("worktree-adopt: both the legacy worktree directory and the worktree claim directory are required")
	}
	outcome, err := decideWorktreeAdoption(ctx, params)
	if err != nil || outcome != WorktreeAdoptAdopted {
		result.Outcome = outcome
		return result, err
	}
	preserved := params.LegacyDir + worktreeAdoptPreservedSuffix
	if ctx.DryRun {
		return previewWorktreeAdoption(ctx, result, preserved), nil
	}
	if err := promoteWorktreeCopy(ctx, params); err != nil {
		return result, err
	}
	return setAsideLegacyWorktree(ctx, result, preserved)
}

// previewWorktreeAdoption traces the copy, promotion and set-aside a real run
// would perform, touching neither volume.
func previewWorktreeAdoption(ctx core.Context, result WorktreeAdoptResult, preserved string) WorktreeAdoptResult {
	staging := filepath.Join(result.ClaimDir, worktreeAdoptStagingName)
	ctx.Trace(fmt.Sprintf("worktree-adopt: dry run: would copy %s into %s and move the copy into %s once it holds .git", result.LegacyDir, staging, result.ClaimDir))
	if _, err := os.Lstat(preserved); err == nil {
		ctx.Trace(fmt.Sprintf("worktree-adopt: dry run: would leave the original at %s because %s already exists", result.LegacyDir, preserved))
		result.Outcome = WorktreeAdoptAdoptedOriginal
		return result
	}
	ctx.Trace(fmt.Sprintf("worktree-adopt: dry run: would set the original aside at %s", preserved))
	result.Outcome = WorktreeAdoptAdopted
	result.PreservedDir = preserved
	return result
}

// decideWorktreeAdoption reports WorktreeAdoptAdopted only when the claim is
// empty and the legacy path holds a real repository; every other outcome is a
// no-op the run explains.
func decideWorktreeAdoption(ctx core.Context, params WorktreeAdoptParams) (WorktreeAdoptOutcome, error) {
	if info, err := os.Stat(params.ClaimDir); err != nil || !info.IsDir() {
		ctx.Info(fmt.Sprintf("worktree-adopt: no worktree volume staged at %s; nothing to adopt", params.ClaimDir))
		return WorktreeAdoptNoClaim, nil
	}
	empty, err := worktreeClaimIsEmpty(params.ClaimDir)
	if err != nil {
		return "", fmt.Errorf("worktree-adopt: read worktree volume %s: %w", params.ClaimDir, err)
	}
	if !empty {
		ctx.Info("worktree-adopt: worktree volume already holds a tree; leaving it untouched")
		return WorktreeAdoptClaimPopulated, nil
	}
	// A symlink here is a sourceless runtime environment's baked release
	// (release-link), not a checkout this volume should take over.
	if info, err := os.Lstat(params.LegacyDir); err != nil || !info.IsDir() {
		ctx.Info(fmt.Sprintf("worktree-adopt: no previous tree at %s; the worktree volume starts empty", params.LegacyDir))
		return WorktreeAdoptNoLegacyTree, nil
	}
	if _, err := os.Stat(filepath.Join(params.LegacyDir, ".git")); err != nil {
		ctx.Info(fmt.Sprintf("worktree-adopt: %s is not a repository; leaving it on the home volume and starting the worktree volume empty", params.LegacyDir))
		return WorktreeAdoptNotARepository, nil
	}
	ctx.Info(fmt.Sprintf("worktree-adopt: adopting the repository at %s onto the worktree volume", params.LegacyDir))
	return WorktreeAdoptAdopted, nil
}

// worktreeClaimIsEmpty treats the claim as empty when it holds nothing an
// operator put there: a freshly provisioned filesystem's lost+found and a
// previous run's abandoned staging dir are both ours to ignore.
func worktreeClaimIsEmpty(claimDir string) (bool, error) {
	entries, err := os.ReadDir(claimDir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "lost+found", worktreeAdoptStagingName:
			continue
		}
		return false, nil
	}
	return true, nil
}

// promoteWorktreeCopy copies the legacy tree into the claim's staging dir and,
// once the copy is proven to hold a repository, moves its entries up into the
// claim itself.
func promoteWorktreeCopy(ctx core.Context, params WorktreeAdoptParams) error {
	staging := filepath.Join(params.ClaimDir, worktreeAdoptStagingName)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("worktree-adopt: clear abandoned staging dir %s: %w", staging, err)
	}
	dirs, err := copyWorktreeTree(ctx, params.LegacyDir, staging)
	if err != nil {
		return fmt.Errorf("worktree-adopt: copy %s; leaving the legacy tree in place: %w", params.LegacyDir, err)
	}
	if _, err := os.Lstat(filepath.Join(staging, ".git")); err != nil {
		return fmt.Errorf("worktree-adopt: copy of %s did not produce a repository; leaving the legacy tree in place", params.LegacyDir)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("worktree-adopt: read staging dir %s: %w", staging, err)
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(params.ClaimDir, entry.Name())); err != nil {
			return fmt.Errorf("worktree-adopt: move %s onto the worktree volume: %w", entry.Name(), err)
		}
	}
	if err := os.Remove(staging); err != nil {
		return fmt.Errorf("worktree-adopt: remove staging dir %s: %w", staging, err)
	}
	if err := applyCopiedDirModes(params.ClaimDir, dirs); err != nil {
		return fmt.Errorf("worktree-adopt: restore directory modes on the worktree volume: %w", err)
	}
	return nil
}

// setAsideLegacyWorktree moves the original aside rather than deleting it:
// losing the tree a second time is the failure adoption exists to prevent. The
// sibling name keeps it visible to the operator, where the masked-under-a-mount
// original was not.
func setAsideLegacyWorktree(ctx core.Context, result WorktreeAdoptResult, preserved string) (WorktreeAdoptResult, error) {
	if _, err := os.Lstat(preserved); err == nil {
		ctx.Info(fmt.Sprintf("worktree-adopt: adopted; leaving the original at %s because %s already exists", result.LegacyDir, preserved))
		result.Outcome = WorktreeAdoptAdoptedOriginal
		return result, nil
	}
	if err := os.Rename(result.LegacyDir, preserved); err != nil {
		return result, fmt.Errorf("worktree-adopt: adopted, but setting the original aside at %s failed: %w", preserved, err)
	}
	if err := os.MkdirAll(result.LegacyDir, 0o755); err != nil {
		return result, fmt.Errorf("worktree-adopt: adopted, but recreating the mount point %s failed: %w", result.LegacyDir, err)
	}
	ctx.Info(fmt.Sprintf("worktree-adopt: adopted; the pre-move copy is preserved at %s", preserved))
	result.Outcome = WorktreeAdoptAdopted
	result.PreservedDir = preserved
	return result, nil
}

// copyWorktreeTree copies src's contents into dst the way `cp -a` would for a
// checkout: directories, regular files with their mode and modification time,
// symlinks as symlinks, and hard links within the tree as hard links. A
// socket, pipe or device has no content to carry and is skipped with a trace.
// Directories stay writable until applyCopiedDirModes runs, so a read-only
// directory still receives its children and can still be moved.
func copyWorktreeTree(ctx core.Context, src, dst string) ([]copiedDir, error) {
	copier := worktreeTreeCopier{ctx: ctx, src: src, dst: dst, links: map[fileLinkKey]string{}}
	if err := filepath.WalkDir(src, copier.visit); err != nil {
		return nil, err
	}
	return copier.dirs, nil
}

// applyCopiedDirModes gives every copied directory below root its source mode
// and modification time, deepest first so a parent's time is set after its
// children stop changing it. The tree's own root takes the claim's mode, not
// the legacy directory's.
func applyCopiedDirModes(root string, dirs []copiedDir) error {
	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		if dir.rel == "." {
			continue
		}
		path := filepath.Join(root, dir.rel)
		if err := os.Chmod(path, dir.info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chtimes(path, dir.info.ModTime(), dir.info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

type copiedDir struct {
	rel  string
	info fs.FileInfo
}

type worktreeTreeCopier struct {
	ctx   core.Context
	src   string
	dst   string
	dirs  []copiedDir
	links map[fileLinkKey]string
}

func (c *worktreeTreeCopier) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	rel, err := filepath.Rel(c.src, path)
	if err != nil {
		return err
	}
	target := filepath.Join(c.dst, rel)
	info, err := entry.Info()
	if err != nil {
		return err
	}
	switch mode := info.Mode(); {
	case mode.IsDir():
		c.dirs = append(c.dirs, copiedDir{rel: rel, info: info})
		return os.MkdirAll(target, 0o700)
	case mode&fs.ModeSymlink != 0:
		return copySymlink(path, target)
	case mode.IsRegular():
		return c.copyRegular(path, target, info)
	default:
		c.ctx.Trace(fmt.Sprintf("worktree-adopt: skipping %s: a %s has no content to copy", path, mode.Type()))
		return nil
	}
}

func (c *worktreeTreeCopier) copyRegular(path, target string, info fs.FileInfo) error {
	if key, ok := fileLinkIdentity(info); ok {
		if first, seen := c.links[key]; seen {
			return os.Link(first, target)
		}
		c.links[key] = target
	}
	return copyRegularFile(path, target, info)
}

func copySymlink(path, target string) error {
	dest, err := os.Readlink(path)
	if err != nil {
		return err
	}
	return os.Symlink(dest, target)
}

func copyRegularFile(path, target string, info fs.FileInfo) (err error) {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	if _, err = out.ReadFrom(in); err != nil {
		return err
	}
	if err = out.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}
