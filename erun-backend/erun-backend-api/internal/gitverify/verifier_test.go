package gitverify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/file"
)

// runGit runs a git command against dir and fails the test on error, so setup
// code stays readable. These are real local git repositories with no network
// or cluster involved, the same style internal/mergeexec/job_test.go used for
// its own real-git tests before this package replaced it.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{}, "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRemoteRepo creates a local git repository with two commits on branch,
// returning its file:// remote URL and both commit hashes (root, tip).
func newRemoteRepo(t *testing.T, branch string) (remoteURL, root, tip string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch="+branch)
	runGit(t, dir, "commit", "--allow-empty", "-m", "root")
	root = runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "--allow-empty", "-m", "tip")
	tip = runGit(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, root, tip
}

func TestRemoteVerifierContainsTip(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	ok, parent, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", tip)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !ok {
		t.Fatalf("expected the branch tip to be reported as contained")
	}
	if parent != root {
		t.Fatalf("parent = %q, want the root commit %q", parent, root)
	}
}

func TestRemoteVerifierContainsAncestor(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")
	_ = tip

	ok, parent, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", root)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !ok {
		t.Fatalf("expected the root commit to be reported as contained (it is an ancestor of the tip)")
	}
	if parent != "" {
		t.Fatalf("parent = %q, want empty for a root commit", parent)
	}
}

func TestRemoteVerifierRefusesCommitNotOnBranch(t *testing.T) {
	remoteURL, _, _ := newRemoteRepo(t, "main")

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=other")
	runGit(t, dir, "commit", "--allow-empty", "-m", "unrelated")
	unrelated := runGit(t, dir, "rev-parse", "HEAD")

	ok, _, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", unrelated)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if ok {
		t.Fatalf("expected a commit from an unrelated repository to be refused as not contained")
	}
}

func TestRemoteVerifierRefusesUnfetchableRemote(t *testing.T) {
	_, _, tip := newRemoteRepo(t, "main")

	_, _, err := NewRemoteVerifier().Contains(context.Background(), "file://"+filepath.Join(t.TempDir(), "does-not-exist"), "main", tip)
	if err == nil {
		t.Fatalf("expected an error fetching a remote that does not exist")
	}
}

func TestRemoteVerifierRejectsInvalidCommitHash(t *testing.T) {
	remoteURL, _, _ := newRemoteRepo(t, "main")

	_, _, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", "not-a-hash")
	if err == nil {
		t.Fatalf("expected an error for a malformed commit hash")
	}
}

func TestRemoteVerifierIsAncestorForDirectAncestor(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", root, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected the root commit to be reported as an ancestor of the tip")
	}
}

// TestRemoteVerifierIsAncestorTolerantOfCommitsInBetween is the property
// erun#2250 depends on: an ancestor commit stays an ancestor of a
// descendant even when unrelated commits (e.g. a release's own pushes) land
// on the branch between them, unlike a strict immediate-parent comparison.
func TestRemoteVerifierIsAncestorTolerantOfCommitsInBetween(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "gated tip")
	gatedTip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "--allow-empty", "-m", "release commit 1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "release commit 2")
	reportedCommit := runGit(t, dir, "rev-parse", "HEAD")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), "file://"+dir, "main", gatedTip, reportedCommit)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected the gated tip to still be reported as an ancestor across the unrelated commits in between")
	}
}

func TestRemoteVerifierIsAncestorTrueForTheSameCommit(t *testing.T) {
	remoteURL, _, tip := newRemoteRepo(t, "main")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", tip, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected a commit to be reported as an ancestor of itself")
	}
}

func TestRemoteVerifierIsAncestorRefusesWhenAncestorNeverLed(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=other")
	runGit(t, dir, "commit", "--allow-empty", "-m", "unrelated")
	unrelated := runGit(t, dir, "rev-parse", "HEAD")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", unrelated, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Fatalf("expected a commit from an unrelated repository to be refused as not an ancestor")
	}

	// The reverse direction is also refused: the tip is not an ancestor of
	// its own root.
	ok, err = NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", tip, root)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Fatalf("expected the tip to be refused as an ancestor of its own root")
	}
}

func TestRemoteVerifierIsAncestorRejectsInvalidCommitHash(t *testing.T) {
	remoteURL, _, tip := newRemoteRepo(t, "main")

	_, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", "not-a-hash", tip)
	if err == nil {
		t.Fatalf("expected an error for a malformed ancestor hash")
	}
}

// TestFetchableRemoteURL pins which forms are rewritten for the verification
// fetch and which are left exactly as the caller wrote them: only a remote
// that would need an identity this process does not have is answered over its
// host's credential-less HTTPS.
func TestFetchableRemoteURL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given string
		want  string
	}{
		{"scp-like, as git remote get-url origin writes it", "git@github.com:sophium/erun.git", "https://github.com/sophium/erun.git"},
		{"scp-like without a user", "github.com:sophium/erun.git", "https://github.com/sophium/erun.git"},
		{"ssh url with a user", "ssh://git@github.com/sophium/erun.git", "https://github.com/sophium/erun.git"},
		{"ssh url without a user", "ssh://github.com/sophium/erun.git", "https://github.com/sophium/erun.git"},
		{"surrounding space", "  git@github.com:sophium/erun.git  ", "https://github.com/sophium/erun.git"},
		{"https is already credential-less", "https://github.com/sophium/erun.git", "https://github.com/sophium/erun.git"},
		{"http is already credential-less", "http://github.com/sophium/erun.git", "http://github.com/sophium/erun.git"},
		{"the git protocol is already credential-less", "git://github.com/sophium/erun.git", "git://github.com/sophium/erun.git"},
		{"file is already credential-less", "file:///srv/git/erun.git", "file:///srv/git/erun.git"},
		{"a local path is not a remote at all", "/srv/git/erun.git", "/srv/git/erun.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fetchableRemoteURL(tc.given)
			if err != nil {
				t.Fatalf("fetchableRemoteURL(%q): %v", tc.given, err)
			}
			if got != tc.want {
				t.Fatalf("fetchableRemoteURL(%q) = %q, want %q", tc.given, got, tc.want)
			}
		})
	}
}

// gitTransportStub stands in at go-git's protocol registry for the "https"
// scheme, so a test can see which URL the verifier hands the transport and
// serve it from a local repository. Serving real history through it is what
// makes "the SSH remote was fetched over HTTPS" an outcome the test observes
// rather than a guess about the shape of a URL.
type gitTransportStub struct {
	repos   map[string]string
	fetched []string
}

func (s *gitTransportStub) NewUploadPackSession(ep *transport.Endpoint, auth transport.AuthMethod) (transport.UploadPackSession, error) {
	s.fetched = append(s.fetched, ep.String())
	dir, ok := s.repos[ep.Host+ep.Path]
	if !ok {
		return nil, fmt.Errorf("this test serves no repository at %s", ep)
	}
	return file.DefaultClient.NewUploadPackSession(&transport.Endpoint{Protocol: "file", Path: dir}, auth)
}

func (s *gitTransportStub) NewReceivePackSession(*transport.Endpoint, transport.AuthMethod) (transport.ReceivePackSession, error) {
	return nil, errors.New("the verifier never pushes")
}

// sshRefusingTransport is the SSH transport an erun runtime without an agent
// has: it fails with the very error the reported MERGE_NOT_VERIFIED carried,
// without touching the network. A verifier that still sends an SSH remote here
// fails its test for the reported reason.
type sshRefusingTransport struct{ reached bool }

func (s *sshRefusingTransport) NewUploadPackSession(*transport.Endpoint, transport.AuthMethod) (transport.UploadPackSession, error) {
	s.reached = true
	return nil, errors.New(`error creating SSH agent: "SSH agent requested but SSH_AUTH_SOCK not-specified"`)
}

func (s *sshRefusingTransport) NewReceivePackSession(*transport.Endpoint, transport.AuthMethod) (transport.ReceivePackSession, error) {
	s.reached = true
	return nil, errors.New(`error creating SSH agent: "SSH agent requested but SSH_AUTH_SOCK not-specified"`)
}

// useTestTransports installs both stubs for the duration of one test and puts
// the real transports back afterwards: go-git's registry is process-wide, so
// one left behind would follow every later test in this package.
func useTestTransports(t *testing.T) (*gitTransportStub, *sshRefusingTransport) {
	t.Helper()
	previous := map[string]transport.Transport{}
	for _, scheme := range []string{"https", "ssh"} {
		c, ok := client.Protocols[scheme]
		if !ok {
			t.Fatalf("go-git registers no %s transport to stand in for", scheme)
		}
		previous[scheme] = c
	}
	t.Cleanup(func() {
		for scheme, c := range previous {
			client.InstallProtocol(scheme, c)
		}
	})

	httpsStub := &gitTransportStub{repos: map[string]string{}}
	sshStub := &sshRefusingTransport{}
	client.InstallProtocol("https", httpsStub)
	client.InstallProtocol("ssh", sshStub)
	return httpsStub, sshStub
}

// TestRemoteVerifierFetchesAnSSHRemoteOverHTTPS reproduces the reported
// refusal: --remote-url set from `git remote get-url origin` — git@github.com:
// owner/repo.git on a repository whose origin is SSH — was sent to a fetch
// with no SSH agent to use, so a merge whose push had already landed came back
// as 409 MERGE_NOT_VERIFIED naming SSH_AUTH_SOCK. Here the caller names its
// origin in exactly that form and the verification completes: the tip that
// reports as contained over https reports as contained over the SSH remote,
// because the SSH form is read over the host's credential-less HTTPS.
func TestRemoteVerifierFetchesAnSSHRemoteOverHTTPS(t *testing.T) {
	httpsStub, sshStub := useTestTransports(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "root")
	root := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "--allow-empty", "-m", "tip")
	tip := runGit(t, dir, "rev-parse", "HEAD")
	httpsStub.repos["git.example.test/erun.git"] = dir

	verifier := NewRemoteVerifier()

	ok, parent, err := verifier.Contains(context.Background(), "git@git.example.test:erun.git", "main", tip)
	if err != nil {
		t.Fatalf("Contains with an SSH remote: %v", err)
	}
	if sshStub.reached {
		t.Fatalf("the SSH remote was fetched over SSH, the transport this runtime has no credentials for")
	}
	if !ok {
		t.Fatalf("expected the branch tip to be reported as contained when the SSH remote is fetched over HTTPS")
	}
	if parent != root {
		t.Fatalf("parent = %q, want the root commit %q", parent, root)
	}

	// IsAncestor walks the same fetch, and it is the other half the queue's
	// own verification needs: the gated tip has to be an ancestor of the
	// reported commit.
	isAncestor, err := verifier.IsAncestor(context.Background(), "git@git.example.test:erun.git", "main", root, tip)
	if err != nil {
		t.Fatalf("IsAncestor with an SSH remote: %v", err)
	}
	if !isAncestor {
		t.Fatalf("expected the root commit to be reported as an ancestor when the SSH remote is fetched over HTTPS")
	}

	// The verifier read the URL it rewrote, not the one it was handed.
	if len(httpsStub.fetched) == 0 || httpsStub.fetched[0] != "https://git.example.test/erun.git" {
		t.Fatalf("fetched %v, want the HTTPS form of the SSH remote", httpsStub.fetched)
	}
}

// TestRemoteVerifierNamesTheRequiredFormForAnSSHRemoteItCannotRewrite: an SSH
// remote on a port of its own has no HTTPS equivalent, so it is refused up
// front, naming the form the platform needs — the failure the report asked for
// instead of one arriving after the push.
func TestRemoteVerifierNamesTheRequiredFormForAnSSHRemoteItCannotRewrite(t *testing.T) {
	httpsStub, sshStub := useTestTransports(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "root")
	tip := runGit(t, dir, "rev-parse", "HEAD")
	httpsStub.repos["git.example.test/erun.git"] = dir

	_, _, err := NewRemoteVerifier().Contains(context.Background(), "ssh://git@git.example.test:2222/erun.git", "main", tip)
	if err == nil {
		t.Fatalf("expected an SSH remote on its own port to be refused")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error %q does not name the form the platform needs", err)
	}
	if sshStub.reached {
		t.Fatalf("the refused remote was still sent to the SSH transport")
	}
}

// TestRemoteVerifierSeparatesAFailedReadFromAFailedMerge: the fetch failing is
// not a verdict that the merge did not land, and the message has to say so —
// the caller is reading a 409 about a branch it has already pushed.
func TestRemoteVerifierSeparatesAFailedReadFromAFailedMerge(t *testing.T) {
	httpsStub, _ := useTestTransports(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "root")
	tip := runGit(t, dir, "rev-parse", "HEAD")
	httpsStub.repos["git.example.test/erun.git"] = dir

	_, _, err := NewRemoteVerifier().Contains(context.Background(), "git@git.example.test:absent.git", "main", tip)
	if err == nil {
		t.Fatalf("expected an error fetching a remote the platform cannot read")
	}
	message := err.Error()
	if !strings.Contains(message, "not judged") {
		t.Fatalf("error %q reads as a verdict on the merge rather than on the platform's read of the remote", message)
	}
	if !strings.Contains(message, "git@git.example.test:absent.git") || !strings.Contains(message, "https://git.example.test/absent.git") {
		t.Fatalf("error %q does not name both the remote given and the form fetched", message)
	}
}
