package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// workingIssueCommandRunner runs a command (git, gh) in dir and returns its
// trimmed stdout. Injected so tests drive the resolver without a real repo or
// network.
type workingIssueCommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// workingIssueCacheTTL bounds how long a resolved working issue is reused. The
// hover card fetches lazily on open; a short TTL keeps the branch/title fresh
// across a `git checkout` without re-running git + gh on every hover.
const workingIssueCacheTTL = 30 * time.Second

type workingIssueCacheEntry struct {
	value     uiWorkingIssue
	expiresAt time.Time
}

// branchIssuePattern matches the repository's branch naming convention
// (feature/<n>-… / bug/<n>-…) so the issue number is parseable from the
// branch. See the root AGENTS.md "Branching Strategy".
var branchIssuePattern = regexp.MustCompile(`^(?:feature|bug)/(\d+)-`)

// parseIssueNumberFromBranch extracts the issue number a branch is working on,
// or 0 when the branch doesn't follow the feature/<n>- / bug/<n>- convention.
func parseIssueNumberFromBranch(branch string) int {
	match := branchIssuePattern.FindStringSubmatch(strings.TrimSpace(branch))
	if match == nil {
		return 0
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return n
}

func execWorkingIssueCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// EnvironmentWorkingIssue resolves what an environment is currently working on
// for the sidebar hover card: the worktree's current git branch and, when the
// branch names an issue, that issue's title. The worktree must be reachable
// from the host, which holds for local-agent envs (worktree mounted from the
// machine). Remote-agent / runtime envs keep their worktree in the pod, so the
// result is marked unavailable with a reason rather than reaching into the pod
// (which is only possible while the env is open). Results are cached per env
// for workingIssueCacheTTL.
func (a *App) EnvironmentWorkingIssue(selection uiSelection) (uiWorkingIssue, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiWorkingIssue{}, nil
	}

	cacheKey := selectionKey(selection)
	if cached, ok := a.lookupWorkingIssue(cacheKey); ok {
		return cached, nil
	}

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiWorkingIssue{}, err
	}

	value := a.resolveWorkingIssue(result.EnvConfig)
	a.storeWorkingIssue(cacheKey, value)
	return value, nil
}

// resolveWorkingIssue is the transport-free core: it runs git in the env's
// host worktree to read the branch, then gh to resolve the linked issue title.
// It never errors out to the caller — an unreachable worktree or a failed
// lookup degrades to an honest empty/partial state the hover card renders.
func (a *App) resolveWorkingIssue(env eruncommon.EnvConfig) uiWorkingIssue {
	if env.RemoteWorktree() {
		return uiWorkingIssue{Available: false, Reason: "worktree lives in the pod"}
	}
	repo := env.EffectiveLocalRepoPath()
	if repo == "" {
		return uiWorkingIssue{Available: false, Reason: "no local worktree path"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := a.deps.runWorkingIssueCommand(ctx, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" || branch == "HEAD" {
		// No branch (not a repo, or detached HEAD). Available, but nothing
		// to show beyond "no branch".
		return uiWorkingIssue{Available: true}
	}

	value := uiWorkingIssue{Available: true, Branch: branch}
	number := parseIssueNumberFromBranch(branch)
	if number == 0 {
		return value
	}
	value.IssueNumber = number

	// `gh` resolves the repo from the worktree's origin remote when run with
	// cmd.Dir set. A failed lookup (offline, gh unauthenticated, issue gone)
	// leaves the number without a title — still useful.
	title, err := a.deps.runWorkingIssueCommand(ctx, repo, "gh", "issue", "view", strconv.Itoa(number), "--json", "title", "-q", ".title")
	if err == nil {
		value.IssueTitle = strings.TrimSpace(title)
	}
	return value
}

func (a *App) lookupWorkingIssue(key string) (uiWorkingIssue, bool) {
	a.workingIssueMu.Lock()
	defer a.workingIssueMu.Unlock()
	entry, ok := a.workingIssueCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return uiWorkingIssue{}, false
	}
	return entry.value, true
}

func (a *App) storeWorkingIssue(key string, value uiWorkingIssue) {
	a.workingIssueMu.Lock()
	defer a.workingIssueMu.Unlock()
	a.workingIssueCache[key] = workingIssueCacheEntry{value: value, expiresAt: time.Now().Add(workingIssueCacheTTL)}
}
