package eruncommon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ContributeRepoHTTPS = "https://github.com/sophium/erun.git"
	ContributeRepoSSH   = "git@github.com:sophium/erun.git"
)

// ContributeClonePath returns the canonical clone target for ERun
// contribute mode ($HOME/git/erun), resolved inside the current env —
// the host for local-agent, the pod for remote-agent.
func ContributeClonePath(homeDir string) (string, error) {
	resolved, err := resolveContributeHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, "git", "erun"), nil
}

// ContributeShimPath returns the path to the `erun` shim that contribute
// tabs prepend to PATH so child processes (claude, codex, etc.) resolve
// the local ERun build script instead of the system-wide binary.
func ContributeShimPath(homeDir string) (string, error) {
	resolved, err := resolveContributeHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, ".erun", "contribute", "bin", "erun"), nil
}

// ContributeShimDir returns the directory holding the `erun` shim that
// contribute tabs prepend to PATH.
func ContributeShimDir(homeDir string) (string, error) {
	shim, err := ContributeShimPath(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Dir(shim), nil
}

// ContributeRunScriptPath returns the path to erun-cli/run.sh inside the
// contribute clone, which the shim forwards to.
func ContributeRunScriptPath(homeDir string) (string, error) {
	clone, err := ContributeClonePath(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(clone, "erun-cli", "run.sh"), nil
}

func resolveContributeHomeDir(homeDir string) (string, error) {
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

// ContributeCloneStatus reports the resolved clone state on disk
// without mutating it.
type ContributeCloneStatus struct {
	Target         string
	ParentDir      string
	AlreadyCloned  bool
	ExistingRemote string
}

// StatFunc is the test seam for stat-style filesystem inspection.
type StatFunc func(path string) (os.FileInfo, error)

// MkdirAllFunc is the test seam for directory creation.
type MkdirAllFunc func(path string, perm os.FileMode) error

// SymlinkFunc is the test seam for symlink creation.
type SymlinkFunc func(target, link string) error

// RemoveFunc is the test seam for file removal.
type RemoveFunc func(path string) error

// ResolveContributeCloneStatus inspects the filesystem for the contribute
// clone target and reports whether it already exists. Pure inspection;
// no side effects.
func ResolveContributeCloneStatus(homeDir string, runGit GitCommandRunnerFunc, statFn StatFunc) (ContributeCloneStatus, error) {
	target, err := ContributeClonePath(homeDir)
	if err != nil {
		return ContributeCloneStatus{}, err
	}
	status := ContributeCloneStatus{Target: target, ParentDir: filepath.Dir(target)}
	if statFn == nil {
		statFn = os.Stat
	}
	if _, err := statFn(filepath.Join(target, ".git")); err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return status, fmt.Errorf("stat %s: %w", filepath.Join(target, ".git"), err)
	}
	if runGit == nil {
		runGit = GitCommandRunner
	}
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := runGit(target, asWriter(stdout), asWriter(stderr), "remote", "get-url", "origin"); err != nil {
		return status, fmt.Errorf("git remote get-url origin (in %s): %w%s", target, err, formatGitCommandStderr(stderr.String()))
	}
	status.AlreadyCloned = true
	status.ExistingRemote = strings.TrimSpace(stdout.String())
	return status, nil
}

// IsERunRemote reports whether remote (the output of `git remote get-url
// origin`) refers to the ERun repository, accepting both HTTPS and SSH
// URLs with or without the .git suffix.
func IsERunRemote(remote string) bool {
	remote = normalizeContributeRemote(remote)
	return remote == "https://github.com/sophium/erun" ||
		remote == "git@github.com:sophium/erun"
}

func normalizeContributeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	return strings.TrimSuffix(remote, "/")
}

// ContributeCloneIO bundles the filesystem seams used by
// RunContributeCloneWithIO.
type ContributeCloneIO struct {
	Stat      StatFunc
	MkdirAll  MkdirAllFunc
	Symlink   SymlinkFunc
	Remove    RemoveFunc
	WriteFile WriteFileFunc
}

// WriteFileFunc is the test seam for writing a file with a permission mode.
type WriteFileFunc func(path string, data []byte, perm os.FileMode) error

// RunContributeClone clones the ERun repository into the env and installs
// an `erun` shim so every `erun` invocation in the contribute shell —
// including those an AI agent spawns as child processes — runs the local
// build script rather than the system-wide binary.
//
// The shim is a wrapper script, not a symlink: run.sh derives its source
// dir from $0, so a symlink-invoked $0 would resolve to the shim dir
// instead of erun-cli/ and the rebuild would fail with "go.mod not found".
//
// The audit and command trace lines it emits are locked by integration
// goldens.
func RunContributeClone(ctx Context, homeDir string, runGit GitCommandRunnerFunc) error {
	return RunContributeCloneWithIO(ctx, homeDir, runGit, ContributeCloneIO{})
}

// RunContributeCloneWithIO is the test-friendly variant of
// RunContributeClone with injectable filesystem helpers.
func RunContributeCloneWithIO(ctx Context, homeDir string, runGit GitCommandRunnerFunc, io ContributeCloneIO) error {
	io = defaultContributeCloneIO(io)
	ctx.Info("==> Cloning ERun for contribute mode")
	status, err := ResolveContributeCloneStatus(homeDir, runGit, io.Stat)
	if err != nil {
		return err
	}
	if status.AlreadyCloned {
		if !IsERunRemote(status.ExistingRemote) {
			return fmt.Errorf("%s exists but origin remote %q does not point at the ERun repository", status.Target, status.ExistingRemote)
		}
		ctx.Info(fmt.Sprintf("==> ERun contribute clone already present at %s", status.Target))
	} else {
		ctx.TraceCommand("", "mkdir", "-p", status.ParentDir)
		if !ctx.DryRun {
			if err := io.MkdirAll(status.ParentDir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", status.ParentDir, err)
			}
		}
		ctx.TraceCommand("", "git", "clone", ContributeRepoHTTPS, status.Target)
		if !ctx.DryRun {
			if runGit == nil {
				runGit = GitCommandRunner
			}
			stdout := new(strings.Builder)
			stderr := new(strings.Builder)
			if err := runGit(status.ParentDir, asWriter(stdout), asWriter(stderr), "clone", ContributeRepoHTTPS, status.Target); err != nil {
				return fmt.Errorf("git clone: %w%s", err, formatGitCommandStderr(stderr.String()))
			}
			ctx.Info(fmt.Sprintf("==> Cloned ERun for contribute mode at %s", status.Target))
		} else {
			ctx.Info(fmt.Sprintf("==> Would clone ERun to %s", status.Target))
		}
	}

	if err := installContributeShim(ctx, homeDir, io); err != nil {
		return err
	}
	return nil
}

func installContributeShim(ctx Context, homeDir string, io ContributeCloneIO) error {
	shimPath, err := ContributeShimPath(homeDir)
	if err != nil {
		return err
	}
	shimDir := filepath.Dir(shimPath)
	target, err := ContributeRunScriptPath(homeDir)
	if err != nil {
		return err
	}
	ctx.TraceCommand("", "mkdir", "-p", shimDir)
	if !ctx.DryRun {
		if err := io.MkdirAll(shimDir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", shimDir, err)
		}
	}
	ctx.TraceCommand("", "write-script", shimPath)
	if !ctx.DryRun {
		// Clear any leftover entry (e.g. a symlink from an earlier shim
		// version) so WriteFile writes a fresh file instead of following
		// an old link.
		_ = io.Remove(shimPath)
		if err := io.WriteFile(shimPath, []byte(contributeShimScript(target)), 0o755); err != nil {
			return fmt.Errorf("write shim %s: %w", shimPath, err)
		}
	}
	ctx.Info(fmt.Sprintf("==> Installed contribute shim at %s", shimPath))
	return nil
}

// contributeShimScript reads $HOME at exec time rather than baking in an
// absolute path, so the shim survives a pod restart that remounts a
// different home volume.
func contributeShimScript(target string) string {
	_ = target // intentionally unused: the script resolves $HOME at runtime
	return "#!/usr/bin/env bash\n" +
		"exec \"$HOME/git/erun/erun-cli/run.sh\" \"$@\"\n"
}

func defaultContributeCloneIO(io ContributeCloneIO) ContributeCloneIO {
	if io.Stat == nil {
		io.Stat = os.Stat
	}
	if io.MkdirAll == nil {
		io.MkdirAll = os.MkdirAll
	}
	if io.Symlink == nil {
		io.Symlink = os.Symlink
	}
	if io.Remove == nil {
		io.Remove = os.Remove
	}
	if io.WriteFile == nil {
		io.WriteFile = os.WriteFile
	}
	return io
}

func asWriter(b *strings.Builder) io.Writer {
	return b
}
