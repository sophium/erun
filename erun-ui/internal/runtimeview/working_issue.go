package runtimeview

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// CommandRunner runs a host command in dir and returns its trimmed stdout.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

const workingIssueCacheTTL = 30 * time.Second

type workingIssueCacheEntry struct {
	value     WorkingIssue
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

// EnvironmentWorkingIssue resolves an environment's current branch and its
// issue title for the sidebar hover card.
func (s *Service) EnvironmentWorkingIssue(selection uimodel.Selection) (WorkingIssue, error) {
	selection = uimodel.NormalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return WorkingIssue{}, nil
	}

	cacheKey := uimodel.SelectionKey(selection)
	if cached, ok := s.lookupWorkingIssue(cacheKey); ok {
		return cached, nil
	}

	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return WorkingIssue{}, err
	}

	value := s.resolveWorkingIssue(result)
	// Cache only resolved work so an unavailable answer flips as soon as the env opens.
	if value.Available {
		s.storeWorkingIssue(cacheKey, value)
	}
	return value, nil
}

func (s *Service) resolveWorkingIssue(result config.OpenResult) WorkingIssue {
	env := result.EnvConfig
	if env.RemoteWorktree() {
		return s.resolvePodWorkingIssue(result)
	}
	repo := env.EffectiveLocalRepoPath()
	if repo == "" {
		return WorkingIssue{Available: false, Reason: "no local worktree path"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := s.d.RunCommand(ctx, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return WorkingIssue{Available: true}
	}
	return s.workingIssueFromBranch(ctx, repo, branch)
}

// resolvePodWorkingIssue needs the port-forward, which exists only while the env is open.
func (s *Service) resolvePodWorkingIssue(result config.OpenResult) WorkingIssue {
	mcpPort := config.MCPPortForResult(result)
	if mcpPort <= 0 || !s.d.CanReachMCPEndpoint(mcpPort) {
		return WorkingIssue{Available: false, Reason: "open this environment to see its in-pod work"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branch, err := s.d.LoadPodBranch(ctx, s.d.MCPEndpoint(result), s.d.MCPBearer(result.Tenant, result.EnvConfig.Name))
	if err != nil {
		return WorkingIssue{Available: false, Reason: "in-pod work is not reachable right now"}
	}
	// gh resolves the title host-side; the project worktree's origin is the repo the pod cloned.
	return s.workingIssueFromBranch(ctx, strings.TrimSpace(result.RepoPath), branch)
}

func (s *Service) workingIssueFromBranch(ctx context.Context, titleRepo, branch string) WorkingIssue {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return WorkingIssue{Available: true}
	}

	value := WorkingIssue{Available: true, Branch: branch}
	number := parseIssueNumberFromBranch(branch)
	if number == 0 {
		return value
	}
	value.IssueNumber = number
	if titleRepo == "" {
		return value
	}

	title, err := s.d.RunCommand(ctx, titleRepo, "gh", "issue", "view", strconv.Itoa(number), "--json", "title", "-q", ".title")
	if err == nil {
		value.IssueTitle = strings.TrimSpace(title)
	}
	return value
}

func (s *Service) lookupWorkingIssue(key string) (WorkingIssue, bool) {
	s.workingIssueMu.Lock()
	defer s.workingIssueMu.Unlock()
	entry, ok := s.workingIssueCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return WorkingIssue{}, false
	}
	return entry.value, true
}

func (s *Service) storeWorkingIssue(key string, value WorkingIssue) {
	s.workingIssueMu.Lock()
	defer s.workingIssueMu.Unlock()
	s.workingIssueCache[key] = workingIssueCacheEntry{value: value, expiresAt: time.Now().Add(workingIssueCacheTTL)}
}
