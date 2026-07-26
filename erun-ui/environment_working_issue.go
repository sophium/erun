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

type workingIssueCommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// A short TTL keeps the branch/title fresh across a `git checkout` without
// re-running git + gh on every hover.
const workingIssueCacheTTL = 30 * time.Second

type workingIssueCacheEntry struct {
	value     uiWorkingIssue
	expiresAt time.Time
}

// branchIssuePattern encodes the repo's branch naming convention (root
// AGENTS.md "Branching Strategy").
var branchIssuePattern = regexp.MustCompile(`^(?:feature|bug)/(\d+)-`)

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
	eruncommon.HideConsoleWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// EnvironmentWorkingIssue resolves what an environment is currently working on
// for the sidebar hover card — its current git branch and, when the branch
// names an issue, that issue's title. Local-agent envs read the host worktree;
// remote/runtime envs read the in-pod worktree while the env is open, otherwise
// reporting an honest open-to-view state.
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

	value := a.resolveWorkingIssue(result)
	// Cache only resolved work: unavailable answers ("open this env…") are a
	// cheap port probe and must flip the moment the env opens, not after the
	// TTL.
	if value.Available {
		a.storeWorkingIssue(cacheKey, value)
	}
	return value, nil
}

func (a *App) resolveWorkingIssue(result eruncommon.OpenResult) uiWorkingIssue {
	env := result.EnvConfig
	if env.RemoteWorktree() {
		return a.resolvePodWorkingIssue(result)
	}
	repo := env.EffectiveLocalRepoPath()
	if repo == "" {
		return uiWorkingIssue{Available: false, Reason: "no local worktree path"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := a.deps.runWorkingIssueCommand(ctx, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// Not a repo. Available, but nothing to show beyond "no branch".
		return uiWorkingIssue{Available: true}
	}
	return a.workingIssueFromBranch(ctx, repo, branch)
}

// resolvePodWorkingIssue reads the in-pod branch for a remote/runtime env. The
// port-forward exists only while the env is open here, so an unreachable port
// means there is nothing to query yet — the honest state prompts the operator
// to open the env.
func (a *App) resolvePodWorkingIssue(result eruncommon.OpenResult) uiWorkingIssue {
	mcpPort := eruncommon.MCPPortForResult(result)
	if mcpPort <= 0 || !a.deps.canConnectLocalPort(mcpPort) {
		return uiWorkingIssue{Available: false, Reason: "open this environment to see its in-pod work"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := a.deps.loadPodBranch(ctx, mcpEndpointForOpenResult(result), a.mcpBearer(result.Tenant, result.EnvConfig.Name))
	if err != nil {
		return uiWorkingIssue{Available: false, Reason: "in-pod work is not reachable right now"}
	}
	// The issue title resolves host-side: gh reads the origin remote of the
	// project worktree, which names the same repository the pod cloned.
	return a.workingIssueFromBranch(ctx, strings.TrimSpace(result.RepoPath), branch)
}

func (a *App) workingIssueFromBranch(ctx context.Context, titleRepo, branch string) uiWorkingIssue {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		// No branch (detached HEAD or empty repo). Available, but nothing to
		// show beyond "no branch".
		return uiWorkingIssue{Available: true}
	}

	value := uiWorkingIssue{Available: true, Branch: branch}
	number := parseIssueNumberFromBranch(branch)
	if number == 0 {
		return value
	}
	value.IssueNumber = number
	if titleRepo == "" {
		return value
	}

	title, err := a.deps.runWorkingIssueCommand(ctx, titleRepo, "gh", "issue", "view", strconv.Itoa(number), "--json", "title", "-q", ".title")
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
