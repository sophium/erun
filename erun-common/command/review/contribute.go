package review

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/gitcmd"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

const (
	ContributeRepoHTTPS = "https://github.com/sophium/erun.git"
	ContributeRepoSSH   = "git@github.com:sophium/erun.git"
)

// ContributeShimPath returns the `erun` shim path contribute tabs prepend to
// PATH so child processes run the local build.
func ContributeShimPath(homeDir string) (string, error) {
	resolved, err := core.ResolveContributeHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, ".erun", "contribute", "bin", "erun"), nil
}

// ContributeRunScriptPath returns erun-cli/run.sh in the contribute clone.
func ContributeRunScriptPath(homeDir string) (string, error) {
	clone, err := core.ContributeClonePath(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(clone, "erun-cli", "run.sh"), nil
}

// ContributeCloneStatus reports the clone state on disk.
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

// ResolveContributeCloneStatus reports whether the contribute clone exists.
func ResolveContributeCloneStatus(homeDir string, runGit gitcmd.GitCommandRunnerFunc, statFn StatFunc) (ContributeCloneStatus, error) {
	target, err := core.ContributeClonePath(homeDir)
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
		runGit = gitx.GitCommandRunner
	}
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := runGit(target, AsWriter(stdout), AsWriter(stderr), "remote", "get-url", "origin"); err != nil {
		return status, fmt.Errorf("git remote get-url origin (in %s): %w%s", target, err, core.FormatGitCommandStderr(stderr.String()))
	}
	status.AlreadyCloned = true
	status.ExistingRemote = strings.TrimSpace(stdout.String())
	return status, nil
}

// IsERunRemote reports whether remote is the ERun repository, over HTTPS or SSH.
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

// ContributeCloneIO bundles the filesystem seams of RunContributeCloneWithIO.
type ContributeCloneIO struct {
	Stat      StatFunc
	MkdirAll  MkdirAllFunc
	Symlink   SymlinkFunc
	Remove    RemoveFunc
	WriteFile WriteFileFunc
}

// WriteFileFunc is the test seam for writing a file with a permission mode.
type WriteFileFunc func(path string, data []byte, perm os.FileMode) error

// RunContributeClone clones ERun and installs an `erun` shim so every erun call
// in the contribute shell, agents included, runs the local build. The shim is a
// script, not a symlink: run.sh derives its source dir from $0. Its trace lines
// are locked by integration goldens.
func RunContributeClone(ctx core.Context, homeDir string, runGit gitcmd.GitCommandRunnerFunc) error {
	return RunContributeCloneWithIO(ctx, homeDir, runGit, ContributeCloneIO{})
}

// RunContributeCloneWithIO is RunContributeClone with injectable filesystem seams.
func RunContributeCloneWithIO(ctx core.Context, homeDir string, runGit gitcmd.GitCommandRunnerFunc, io ContributeCloneIO) error {
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
	} else if err := cloneContributeRepository(ctx, status, runGit, io); err != nil {
		return err
	}

	if err := installContributeShim(ctx, homeDir, io); err != nil {
		return err
	}
	return nil
}

func cloneContributeRepository(ctx core.Context, status ContributeCloneStatus, runGit gitcmd.GitCommandRunnerFunc, io ContributeCloneIO) error {
	ctx.TraceCommand("", "mkdir", "-p", status.ParentDir)
	if !ctx.DryRun {
		if err := io.MkdirAll(status.ParentDir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", status.ParentDir, err)
		}
	}
	ctx.TraceCommand("", "git", "clone", ContributeRepoHTTPS, status.Target)
	if ctx.DryRun {
		ctx.Info(fmt.Sprintf("==> Would clone ERun to %s", status.Target))
		return nil
	}
	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := runGit(status.ParentDir, AsWriter(stdout), AsWriter(stderr), "clone", ContributeRepoHTTPS, status.Target); err != nil {
		return fmt.Errorf("git clone: %w%s", err, core.FormatGitCommandStderr(stderr.String()))
	}
	ctx.Info(fmt.Sprintf("==> Cloned ERun for contribute mode at %s", status.Target))
	return nil
}

func installContributeShim(ctx core.Context, homeDir string, io ContributeCloneIO) error {
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
		// Remove first so WriteFile does not follow an old symlink shim.
		_ = io.Remove(shimPath)
		if err := io.WriteFile(shimPath, []byte(contributeShimScript(target)), 0o755); err != nil {
			return fmt.Errorf("write shim %s: %w", shimPath, err)
		}
	}
	ctx.Info(fmt.Sprintf("==> Installed contribute shim at %s", shimPath))
	return nil
}

// contributeShimScript reads $HOME at exec time so the shim survives a
// remounted home volume.
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

func AsWriter(b *strings.Builder) io.Writer {
	return b
}
