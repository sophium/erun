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
// branch names an issue, that issue's title. Local-agent envs read the host
// worktree; remote-agent / runtime envs read the in-pod worktree over the
// env's MCP port-forward while it is reachable, and report an honest
// open-to-view state otherwise (issue #462). Resolved results are cached per
// env for workingIssueCacheTTL.
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

// resolveWorkingIssue is the transport-free core: it reads the env worktree's
// current branch — from the host worktree for local-agent envs, from inside
// the pod for remote/runtime envs — then resolves the linked issue title. It
// never errors out to the caller — an unreachable worktree or a failed
// lookup degrades to an honest empty/partial state the hover card renders.
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

// resolvePodWorkingIssue reads the in-pod branch over the env's MCP
// port-forward (issue #462: the card used to show the implementation excuse
// "worktree lives in the pod" instead of the work). Reachability is the
// existing canConnectLocalPort signal — the port-forward only exists while
// the env is open in this desktop, so an unreachable port means there is
// nothing to query yet, and the honest state is the next step the user can
// take.
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

// workingIssueFromBranch maps a resolved branch to the hover card's read
// model: the branch itself, the issue number the branch names (per the
// feature/<n>- / bug/<n>- convention), and — when titleRepo is set — the
// issue title via gh. A failed title lookup (offline, gh unauthenticated,
// issue gone) leaves the number without a title — still useful.
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
