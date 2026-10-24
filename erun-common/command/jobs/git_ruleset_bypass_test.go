package jobs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
)

// assertBypassReported holds the report to what an operator needs: which remote
// accepted the push, which ref's rules were stepped over, which rules they
// were, and the command that resolves it afterwards.
func assertBypassReported(t *testing.T, got, wantRef string) {
	t.Helper()
	for _, want := range []string{"origin", wantRef, "Changes must be made through a pull request", "reconcile-bypass"} {
		if !strings.Contains(got, want) {
			t.Errorf("a push admitted by a ruleset bypass must say so in erun's own output, naming %q; output was:\n%s", want, got)
		}
	}
}

// releaseBypassTestContext captures erun's own report while still giving the
// release's git commands the stderr sink a real run has. Leaving it nil is a
// shape production never produces, and a panic on the sink would prove nothing
// about the report this exists to check.
func releaseBypassTestContext(out io.Writer) core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, out, out),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

// installBypassNoticeHook gives a bare remote GitHub's own answer to a push: a
// pre-receive hook whose stderr git relays to the pushing side in the same
// `remote: `-prefixed, blank-line-separated notice GitHub writes. Only a
// receiving remote ever produces that notice, so a hook is the one way to make
// a real `git push` carry a bypass -- which is what makes the claim push's
// report reachable without an injected git seam to stand in for its remote.
func installBypassNoticeHook(t *testing.T, remote, ref string) {
	t.Helper()
	hook := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' 'Bypassed rule violations for %s:' '' '- Changes must be made through a pull request.' '' >&2\nexit 0\n", ref)
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatalf("install the bypass-notice hook: %v", err)
	}
}

// TestReleaseClaimPushReportsAGitHubRulesetBypass drives the repository-global
// claim push against a real remote, because that is the one push erun makes
// with no injected git seam to stand in for one. It is the independently-driven
// half of this report: the release-stage tests above stub git's stderr, so this
// is what proves the notice survives a real push's own stderr plumbing, and
// that the claim ref -- which lands on the same origin as the release's own
// refs -- is not the push left silent.
func TestReleaseClaimPushReportsAGitHubRulesetBypass(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	ref := eruncommon.ReleaseRepoClaimRef("1.4.2")
	installBypassNoticeHook(t, remote, ref)

	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)

	sha, err := core.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("resolve the commit the claim push names: %v", err)
	}
	if err := eruncommon.GitPushCreateRef(ctx, repo, "origin", strings.TrimSpace(string(sha)), ref); err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), ref)
}
