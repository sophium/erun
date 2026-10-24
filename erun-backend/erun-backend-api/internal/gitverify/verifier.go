// Package gitverify confirms a reported merge actually landed on the tenant's
// real git remote instead of trusting the reporter.
package gitverify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Verifier checks commits against a branch on a remote.
type Verifier interface {
	// Contains reports whether commit is reachable from branch's tip, with its parent hash.
	Contains(ctx context.Context, remoteURL, branch, commit string) (ok bool, parent string, err error)

	// IsAncestor reports whether ancestor is reachable from descendant, not
	// whether it is the immediate parent, so commits landing in between are tolerated.
	IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (isAncestor bool, err error)

	// ContainsChanges reports whether sourceBranch's work is in targetBranch's
	// history even when its commits are not, as after a squash merge.
	ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (contained bool, commit string, err error)
}

// RemoteVerifier fetches anonymously with go-git; fetchableRemoteURL rewrites
// remotes that would need credentials.
type RemoteVerifier struct{}

func NewRemoteVerifier() *RemoteVerifier { return &RemoteVerifier{} }

func (RemoteVerifier) Contains(ctx context.Context, remoteURL, branch, commit string) (bool, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	branch = strings.TrimSpace(branch)
	commit = strings.TrimSpace(commit)
	if remoteURL == "" || branch == "" || commit == "" {
		return false, "", fmt.Errorf("remoteURL, branch, and commit are all required")
	}
	if !plumbing.IsHash(commit) {
		return false, "", fmt.Errorf("commit %q is not a git commit hash", commit)
	}

	repo, err := fetchBranch(ctx, remoteURL, branch)
	if err != nil {
		return false, "", err
	}
	tipCommit, err := branchTip(repo, branch)
	if err != nil {
		return false, "", err
	}

	targetCommit, ok := fetchedCommit(repo, commit)
	if !ok {
		return false, "", nil
	}
	return commitReachesTip(targetCommit, tipCommit)
}

func normalizeAncestorArgs(remoteURL, branch, ancestor, descendant string) (string, string, string, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	branch = strings.TrimSpace(branch)
	ancestor = strings.TrimSpace(ancestor)
	descendant = strings.TrimSpace(descendant)
	if remoteURL == "" || branch == "" || ancestor == "" || descendant == "" {
		return "", "", "", "", fmt.Errorf("remoteURL, branch, ancestor, and descendant are all required")
	}
	if !plumbing.IsHash(ancestor) {
		return "", "", "", "", fmt.Errorf("commit %q is not a git commit hash", ancestor)
	}
	if !plumbing.IsHash(descendant) {
		return "", "", "", "", fmt.Errorf("commit %q is not a git commit hash", descendant)
	}
	return remoteURL, branch, ancestor, descendant, nil
}

func (RemoteVerifier) IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (bool, error) {
	remoteURL, branch, ancestor, descendant, err := normalizeAncestorArgs(remoteURL, branch, ancestor, descendant)
	if err != nil {
		return false, err
	}
	if ancestor == descendant {
		return true, nil
	}

	repo, err := fetchBranch(ctx, remoteURL, branch)
	if err != nil {
		return false, err
	}
	ancestorCommit, ok := fetchedCommit(repo, ancestor)
	if !ok {
		return false, nil
	}
	descendantCommit, err := repo.CommitObject(plumbing.NewHash(descendant))
	if err != nil {
		return false, err
	}
	return ancestorCommit.IsAncestor(descendantCommit)
}

// ContainsChanges answers an ordinary or fast-forward landing by ancestry
// before comparing change sets.
func (RemoteVerifier) ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (bool, string, error) {
	remoteURL, targetBranch, sourceBranch, err := normalizeChangeArgs(remoteURL, targetBranch, sourceBranch)
	if err != nil {
		return false, "", err
	}

	repo, err := fetchBranches(ctx, remoteURL, targetBranch, sourceBranch)
	if err != nil {
		return false, "", err
	}
	targetTip, err := branchTip(repo, targetBranch)
	if err != nil {
		return false, "", err
	}
	sourceTip, err := branchTip(repo, sourceBranch)
	if err != nil {
		return false, "", err
	}
	return branchLandedInTarget(repo, targetBranch, sourceBranch, targetTip, sourceTip)
}

func normalizeChangeArgs(remoteURL, targetBranch, sourceBranch string) (string, string, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	targetBranch = strings.TrimSpace(targetBranch)
	sourceBranch = strings.TrimSpace(sourceBranch)
	if remoteURL == "" || targetBranch == "" || sourceBranch == "" {
		return "", "", "", fmt.Errorf("remoteURL, targetBranch, and sourceBranch are all required")
	}
	if targetBranch == sourceBranch {
		return "", "", "", fmt.Errorf("targetBranch and sourceBranch are both %q", targetBranch)
	}
	return remoteURL, targetBranch, sourceBranch, nil
}

// branchLandedInTarget checks ancestry first, then a squash landing. An equal
// tip counts as contained: it is indistinguishable from a fast-forward landing,
// and refusing it would only strand real landings.
func branchLandedInTarget(repo *git.Repository, targetBranch, sourceBranch string, targetTip, sourceTip *object.Commit) (bool, string, error) {
	alreadyLanded, err := sourceTip.IsAncestor(targetTip)
	if err != nil {
		return false, "", err
	}
	if alreadyLanded {
		return true, sourceTip.Hash.String(), nil
	}

	wanted, base, err := branchChangeFingerprint(targetBranch, sourceBranch, targetTip, sourceTip)
	if err != nil || wanted == "" {
		return false, "", err
	}
	landed, err := findSquashedLanding(repo, targetTip, base, wanted)
	if err != nil || landed == "" {
		return false, "", err
	}
	return true, landed, nil
}

// branchChangeFingerprint returns the branch's change set and merge base. An
// empty fingerprint (no single merge base, or no changes) is no evidence of landing.
func branchChangeFingerprint(targetBranch, sourceBranch string, targetTip, sourceTip *object.Commit) (string, *object.Commit, error) {
	bases, err := sourceTip.MergeBase(targetTip)
	if err != nil {
		return "", nil, fmt.Errorf("finding the merge base of %s and %s: %w", sourceBranch, targetBranch, err)
	}
	if len(bases) != 1 {
		return "", nil, nil
	}
	wanted, err := changeFingerprint(bases[0], sourceTip)
	if err != nil {
		return "", nil, err
	}
	return wanted, bases[0], nil
}

func findSquashedLanding(repo *git.Repository, targetTip, base *object.Commit, wanted string) (string, error) {
	iter, err := repo.Log(&git.LogOptions{From: targetTip.Hash})
	if err != nil {
		return "", err
	}
	landed := ""
	err = iter.ForEach(func(candidate *object.Commit) error {
		carries, err := commitCarriesChange(candidate, base, wanted)
		if err != nil {
			return err
		}
		if !carries {
			return nil
		}
		landed = candidate.Hash.String()
		return storer.ErrStop
	})
	if err != nil && !errors.Is(err, storer.ErrStop) {
		return "", err
	}
	return landed, nil
}

// Commits at or before the merge base are shared history, and merge commits
// have no single change to compare; a squash commit always has one parent.
func commitCarriesChange(candidate, base *object.Commit, wanted string) (bool, error) {
	if candidate.Hash == base.Hash || len(candidate.ParentHashes) != 1 {
		return false, nil
	}
	atOrBeforeBase, err := candidate.IsAncestor(base)
	if err != nil {
		return false, err
	}
	if atOrBeforeBase {
		return false, nil
	}
	parent, err := candidate.Parent(0)
	if err != nil {
		return false, err
	}
	fingerprint, err := changeFingerprint(parent, candidate)
	if err != nil {
		return false, err
	}
	return fingerprint == wanted, nil
}

// changeFingerprint keys each path by the lines it adds and removes, not by
// blob, so a target edit elsewhere in the file does not hide a squash landing.
func changeFingerprint(from, to *object.Commit) (string, error) {
	fromTree, err := from.Tree()
	if err != nil {
		return "", err
	}
	toTree, err := to.Tree()
	if err != nil {
		return "", err
	}
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return "", err
		}
		removed, added, err := changeLineChange(change)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s %s %s %s",
			action,
			change.From.Name, change.From.TreeEntry.Mode, removed,
			change.To.Name, change.To.TreeEntry.Mode, added))
	}
	// Sorted so diff order does not matter; an empty set means identical trees.
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

func changeLineChange(change *object.Change) (removed, added string, err error) {
	fromFile, toFile, err := change.Files()
	if err != nil {
		return "", "", err
	}
	if fromFile == nil && toFile == nil {
		return change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String(), nil
	}
	fromLines, fromText, err := textLines(fromFile)
	if err != nil {
		return "", "", err
	}
	toLines, toText, err := textLines(toFile)
	if err != nil {
		return "", "", err
	}
	if !fromText || !toText {
		return change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String(), nil
	}
	removedLines, addedLines := lineDelta(fromLines, toLines)
	return lineIdentity(removedLines), lineIdentity(addedLines), nil
}

// textLines returns ok=false for non-text files; a missing side has no lines.
func textLines(file *object.File) (lines []string, ok bool, err error) {
	if file == nil {
		return nil, true, nil
	}
	isBinary, err := file.IsBinary()
	if err != nil {
		return nil, false, err
	}
	if isBinary {
		return nil, false, nil
	}
	content, err := file.Contents()
	if err != nil {
		return nil, false, err
	}
	return strings.Split(content, "\n"), true, nil
}

// lineIdentity quotes each line so its bytes cannot be read as separators.
func lineIdentity(lines []string) string {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	return fmt.Sprintf("%q", strings.Join(sorted, "\n"))
}

// maxAlignedLines caps alignment; past it the span is reported whole, which can
// only refuse more, never accept more.
const maxAlignedLines = 4_000_000

// lineDelta returns removed and added lines without positions, so an edit
// shifted down the file still matches.
func lineDelta(from, to []string) (removed, added []string) {
	head := commonHeadLines(from, to)
	from, to = from[head:], to[head:]
	tail := commonTailLines(from, to)
	from, to = from[:len(from)-tail], to[:len(to)-tail]

	switch {
	case len(from) == 0:
		return nil, append([]string(nil), to...)
	case len(to) == 0:
		return append([]string(nil), from...), nil
	case int64(len(from))*int64(len(to)) > maxAlignedLines:
		return append([]string(nil), from...), append([]string(nil), to...)
	}
	return alignLines(from, to)
}

func alignLines(from, to []string) (removed, added []string) {
	width := len(to) + 1
	shared := sharedLineCounts(from, to)
	i, j := 0, 0
	for i < len(from) && j < len(to) {
		switch {
		case from[i] == to[j]:
			i++
			j++
		case shared[(i+1)*width+j] >= shared[i*width+j+1]:
			removed = append(removed, from[i])
			i++
		default:
			added = append(added, to[j])
			j++
		}
	}
	removed = append(removed, from[i:]...)
	added = append(added, to[j:]...)
	return removed, added
}

// sharedLineCounts is the suffix LCS table, indexed shared[i*(len(to)+1)+j].
func sharedLineCounts(from, to []string) []int32 {
	width := len(to) + 1
	shared := make([]int32, (len(from)+1)*width)
	for i := len(from) - 1; i >= 0; i-- {
		for j := len(to) - 1; j >= 0; j-- {
			switch {
			case from[i] == to[j]:
				shared[i*width+j] = shared[(i+1)*width+j+1] + 1
			case shared[(i+1)*width+j] >= shared[i*width+j+1]:
				shared[i*width+j] = shared[(i+1)*width+j]
			default:
				shared[i*width+j] = shared[i*width+j+1]
			}
		}
	}
	return shared
}

func commonHeadLines(from, to []string) int {
	shared := 0
	for shared < len(from) && shared < len(to) && from[shared] == to[shared] {
		shared++
	}
	return shared
}

func commonTailLines(from, to []string) int {
	shared := 0
	for shared < len(from) && shared < len(to) &&
		from[len(from)-1-shared] == to[len(to)-1-shared] {
		shared++
	}
	return shared
}

func fetchBranch(ctx context.Context, remoteURL, branch string) (*git.Repository, error) {
	return fetchBranches(ctx, remoteURL, branch)
}

func fetchBranches(ctx context.Context, remoteURL string, branches ...string) (*git.Repository, error) {
	fetchURL, err := fetchableRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return nil, err
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{fetchURL}})
	if err != nil {
		return nil, err
	}
	refSpecs := make([]config.RefSpec, 0, len(branches))
	for _, branch := range branches {
		refSpecs = append(refSpecs, config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)))
	}
	if err := remote.FetchContext(ctx, &git.FetchOptions{RefSpecs: refSpecs}); err != nil {
		return nil, fmt.Errorf("fetching %s from %s: %w. %s", strings.Join(branches, ", "), fetchURL, err,
			unreadableRemoteNote(remoteURL, fetchURL))
	}
	return repo, nil
}

// fetchableRemoteURL rewrites SSH remotes to the host's anonymous HTTPS form
// since the verifier holds no key; other URLs pass through. An SSH remote on a
// custom port has no HTTPS equivalent and is refused.
func fetchableRemoteURL(remoteURL string) (string, error) {
	given := strings.TrimSpace(remoteURL)
	if given == "" {
		return "", fmt.Errorf("remoteURL is required")
	}
	if rest, ok := strings.CutPrefix(given, "ssh://"); ok {
		return sshRemoteAsHTTPS(remoteURL, rest)
	}
	if strings.Contains(given, "://") {
		return given, nil
	}
	return scpLikeRemoteAsHTTPS(given), nil
}

func sshRemoteAsHTTPS(remoteURL, rest string) (string, error) {
	host, path, found := strings.Cut(trimUser(rest), "/")
	if !found || host == "" || path == "" {
		return "", fmt.Errorf("remote-url %q names neither an ssh host nor a repository path", remoteURL)
	}
	if strings.ContainsRune(host, ':') {
		return "", fmt.Errorf("remote-url %q is an ssh remote on a port of its own, which has no HTTPS equivalent: pass the repository's HTTPS URL, because the platform fetches the target remote without credentials", remoteURL)
	}
	return "https://" + host + "/" + path, nil
}

// A local path or Windows drive has a separator in its host part and is not scp-like.
func scpLikeRemoteAsHTTPS(given string) string {
	hostPart, path, found := strings.Cut(given, ":")
	if !found || path == "" || strings.HasPrefix(path, `\`) {
		return given
	}
	host := trimUser(hostPart)
	if host == "" || strings.ContainsAny(host, `/\`) {
		return given
	}
	return "https://" + host + "/" + strings.TrimPrefix(path, "/")
}

func trimUser(hostPart string) string {
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		return hostPart[at+1:]
	}
	return hostPart
}

// unreadableRemoteNote makes clear a failed fetch is not a verdict that the
// merge did not land; the caller's push has already happened.
func unreadableRemoteNote(remoteURL, fetchURL string) string {
	if strings.TrimSpace(remoteURL) != fetchURL {
		return fmt.Sprintf("the platform reads the target remote over HTTPS without credentials, so remote-url %q was fetched as %s; whether the merge landed was not judged by this failure",
			strings.TrimSpace(remoteURL), fetchURL)
	}
	return "the platform fetches the target remote without credentials; whether the merge landed was not judged by this failure"
}

func fetchedCommit(repo *git.Repository, commit string) (*object.Commit, bool) {
	found, err := repo.CommitObject(plumbing.NewHash(commit))
	return found, err == nil
}

func branchTip(repo *git.Repository, branch string) (*object.Commit, error) {
	tipRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return nil, fmt.Errorf("reading the fetched tip of %s: %w", branch, err)
	}
	tipCommit, err := repo.CommitObject(tipRef.Hash())
	if err != nil {
		return nil, err
	}
	return tipCommit, nil
}

// commitReachesTip reports whether target is tip or an ancestor, with target's parent hash.
func commitReachesTip(target, tip *object.Commit) (bool, string, error) {
	parent := ""
	if len(target.ParentHashes) > 0 {
		parent = target.ParentHashes[0].String()
	}
	if target.Hash == tip.Hash {
		return true, parent, nil
	}
	isAncestor, err := target.IsAncestor(tip)
	if err != nil {
		return false, "", err
	}
	return isAncestor, parent, nil
}
