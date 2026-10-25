package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

type workingIssueCommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

const workingIssueCacheTTL = 30 * time.Second

type workingIssueCacheEntry struct {
	value     uiWorkingIssue
	expiresAt time.Time
}

// branchIssuePattern encodes root AGENTS.md "Branching Strategy".
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
	core.HideConsoleWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// EnvironmentWorkingIssue resolves an environment's current branch and its
// issue title for the sidebar hover card.
func (a *App) EnvironmentWorkingIssue(selection uiSelection) (uiWorkingIssue, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiWorkingIssue{}, nil
	}

	cacheKey := selectionKey(selection)
	if cached, ok := a.lookupWorkingIssue(cacheKey); ok {
		return cached, nil
	}

	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiWorkingIssue{}, err
	}

	value := a.resolveWorkingIssue(result)
	// Cache only resolved work so an unavailable answer flips as soon as the env opens.
	if value.Available {
		a.storeWorkingIssue(cacheKey, value)
	}
	return value, nil
}

func (a *App) resolveWorkingIssue(result config.OpenResult) uiWorkingIssue {
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
		return uiWorkingIssue{Available: true}
	}
	return a.workingIssueFromBranch(ctx, repo, branch)
}

// resolvePodWorkingIssue needs the port-forward, which exists only while the env is open.
func (a *App) resolvePodWorkingIssue(result config.OpenResult) uiWorkingIssue {
	mcpPort := config.MCPPortForResult(result)
	if mcpPort <= 0 || !a.deps.canReachMCPEndpoint(mcpPort) {
		return uiWorkingIssue{Available: false, Reason: "open this environment to see its in-pod work"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := a.deps.loadPodBranch(ctx, mcpEndpointForOpenResult(result), a.mcpBearer(result.Tenant, result.EnvConfig.Name))
	if err != nil {
		return uiWorkingIssue{Available: false, Reason: "in-pod work is not reachable right now"}
	}
	// gh resolves the title host-side; the project worktree's origin is the repo the pod cloned.
	return a.workingIssueFromBranch(ctx, strings.TrimSpace(result.RepoPath), branch)
}

func (a *App) workingIssueFromBranch(ctx context.Context, titleRepo, branch string) uiWorkingIssue {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
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
