package gitx

import (
	"fmt"
	"io"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func GitCurrentBranch(ctx core.Context, projectRoot string) (string, error) {
	ctx.TraceCommand("", "git", "-C", projectRoot, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := core.Command("git", "-C", projectRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GitShortCommit(ctx core.Context, projectRoot string) (string, error) {
	ctx.TraceCommand("", "git", "-C", projectRoot, "rev-parse", "--short", "HEAD")
	output, err := core.Command("git", "-C", projectRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GitCommandRunner(dir string, stdout, stderr io.Writer, args ...string) error {
	cmd := core.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = gitCommandEnv(dir)
	return cmd.Run()
}

func gitCommandEnv(dir string) []string {
	env := os.Environ()

	authorName := firstNonEmptyEnv(env, "GIT_AUTHOR_NAME")
	authorEmail := firstNonEmptyEnv(env, "GIT_AUTHOR_EMAIL")
	committerName := firstNonEmptyEnv(env, "GIT_COMMITTER_NAME")
	committerEmail := firstNonEmptyEnv(env, "GIT_COMMITTER_EMAIL")

	name := authorName
	if name == "" {
		name = committerName
	}
	if name == "" {
		name = gitConfigValue(dir, "user.name")
	}
	if name == "" {
		name = firstNonEmptyEnv(env, "ERUN_GIT_USER_NAME")
	}
	if name == "" {
		name = configpkg.DefaultReleaseGitUserName
	}

	email := authorEmail
	if email == "" {
		email = committerEmail
	}
	if email == "" {
		email = gitConfigValue(dir, "user.email")
	}
	if email == "" {
		email = firstNonEmptyEnv(env, "ERUN_GIT_USER_EMAIL")
	}
	if email == "" {
		email = configpkg.DefaultReleaseGitUserEmail
	}

	env = appendOrReplaceEnv(env, "GIT_AUTHOR_NAME", name)
	env = appendOrReplaceEnv(env, "GIT_AUTHOR_EMAIL", email)
	env = appendOrReplaceEnv(env, "GIT_COMMITTER_NAME", name)
	env = appendOrReplaceEnv(env, "GIT_COMMITTER_EMAIL", email)

	return env
}

func gitConfigValue(dir, key string) string {
	cmd := core.Command("git", "config", "--get", key)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func firstNonEmptyEnv(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(entry, prefix))
		if value != "" {
			return value
		}
	}
	return ""
}

func appendOrReplaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		env[i] = prefix + value
		return env
	}
	return append(env, prefix+value)
}
