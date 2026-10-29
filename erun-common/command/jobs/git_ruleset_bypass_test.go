package jobs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseclaim"
)

func assertBypassReported(t *testing.T, got, wantRef string) {
	t.Helper()
	for _, want := range []string{"origin", wantRef, "Changes must be made through a pull request", "reconcile-bypass"} {
		if !strings.Contains(got, want) {
			t.Errorf("a push admitted by a ruleset bypass must say so in erun's own output, naming %q; output was:\n%s", want, got)
		}
	}
}

// A nil stderr sink is a shape production never produces.
func releaseBypassTestContext(out io.Writer) core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, out, out),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

// installBypassNoticeHook makes a bare remote emit GitHub's `remote: ` bypass
// notice from a pre-receive hook, so a real push carries it.
func installBypassNoticeHook(t *testing.T, remote, ref string) {
	t.Helper()
	hook := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' 'Bypassed rule violations for %s:' '' '- Changes must be made through a pull request.' '' >&2\nexit 0\n", ref)
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatalf("install the bypass-notice hook: %v", err)
	}
}

// The claim push has no git seam, so this proves the notice survives a real
// push's stderr plumbing.
func TestReleaseClaimPushReportsAGitHubRulesetBypass(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	ref := releaseclaim.ReleaseRepoClaimRef("1.4.2")
	installBypassNoticeHook(t, remote, ref)

	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)

	sha, err := core.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("resolve the commit the claim push names: %v", err)
	}
	if err := releaseclaim.GitPushCreateRef(ctx, repo, "origin", strings.TrimSpace(string(sha)), ref); err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), ref)
}
