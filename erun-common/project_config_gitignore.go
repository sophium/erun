package eruncommon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProjectConfigGitIgnoreFix is the one-line remedy for a gitignored
// .erun/config.yaml: a bare ".erun/" directory ignore cannot be negated per
// file in git, so the fix carves the file back out with a glob instead of a
// bare directory pattern.
const ProjectConfigGitIgnoreFix = `replace a bare ".erun/" ignore with ".erun/*" plus "!.erun/config.yaml"`

// ProjectConfigGitIgnored reports whether projectRoot's per-project config
// file (.erun/config.yaml) exists on disk and, if so, whether it is excluded
// by the project's own gitignore rules. erun-docs/reference/configuration.md
// documents this file as the per-project layer, "edited by team in PRs" — a
// gitignored copy is invisible to `git status` and therefore to every
// teammate but the one machine that wrote it, so a build or deploy driven by
// it works there and nowhere else. This shells out to `git check-ignore`
// rather than reimplementing gitignore's own matching rules (nested files,
// negation-inside-an-ignored-directory, .git/info/exclude) a second time.
//
// A detection failure (no git repository, git missing) reports present=false
// silently: the caller already resolved the config from disk, so a git error
// here should not add noise on top of that.
func ProjectConfigGitIgnored(ctx Context, projectRoot string) (present, ignored bool, err error) {
	configFilePath, pathErr := projectConfigPath(projectRoot)
	if pathErr != nil {
		return false, false, nil
	}
	if _, statErr := os.Stat(configFilePath); statErr != nil {
		return false, false, nil
	}

	relPath := filepath.Join(projectConfigDir, configFile)
	ctx.TraceCommand(projectRoot, "git", "check-ignore", "-q", "--", relPath)
	cmd := Command("git", "-C", projectRoot, "check-ignore", "-q", "--", relPath)
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

// WarnIfProjectConfigGitIgnored traces a one-line warning when projectRoot's
// per-project config resolves from disk but git would never track it, so the
// caller's resolution is reproducible only on this machine. Detection errors
// stay silent — see ProjectConfigGitIgnored.
func WarnIfProjectConfigGitIgnored(ctx Context, projectRoot string) {
	if strings.TrimSpace(projectRoot) == "" {
		return
	}
	_, ignored, err := ProjectConfigGitIgnored(ctx, projectRoot)
	if err != nil || !ignored {
		return
	}
	ctx.Trace("config: .erun/config.yaml is excluded by .gitignore -- this resolves only on this machine; " + ProjectConfigGitIgnoreFix)
}
