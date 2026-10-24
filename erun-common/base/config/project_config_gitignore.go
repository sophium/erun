package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// ProjectConfigGitIgnoreFix is the remedy: git cannot negate a file inside a bare
// ignored directory, so the directory pattern must become a glob.
const ProjectConfigGitIgnoreFix = `replace a bare ".erun/" ignore with ".erun/*" plus "!.erun/config.yaml"`

// ProjectConfigGitIgnored reports whether .erun/config.yaml exists and git ignores
// it; detection failures report present=false silently.
func ProjectConfigGitIgnored(ctx core.Context, projectRoot string) (present, ignored bool, err error) {
	if !projectConfigFileExists(projectRoot) {
		return false, false, nil
	}

	relPath := filepath.Join(ProjectConfigDir, ConfigFile)
	ctx.TraceCommand(projectRoot, "git", "check-ignore", "-q", "--", relPath)
	cmd := core.Command("git", "-C", projectRoot, "check-ignore", "-q", "--", relPath)
	runErr := cmd.Run()
	if runErr == nil {
		return true, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
		return true, false, nil
	}
	return true, false, runErr
}

func projectConfigFileExists(projectRoot string) bool {
	configFilePath, err := projectConfigPath(projectRoot)
	if err != nil {
		return false
	}
	_, err = os.Stat(configFilePath)
	return err == nil
}

// WarnIfProjectConfigGitIgnored traces a warning when the project config is gitignored.
func WarnIfProjectConfigGitIgnored(ctx core.Context, projectRoot string) {
	if strings.TrimSpace(projectRoot) == "" {
		return
	}
	_, ignored, err := ProjectConfigGitIgnored(ctx, projectRoot)
	if err != nil || !ignored {
		return
	}
	ctx.Trace("config: .erun/config.yaml is excluded by .gitignore -- this resolves only on this machine; " + ProjectConfigGitIgnoreFix)
}
