package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitHubBypassNotice is the stderr a real `git push` to a protected branch
// produces when the pushing credential holds a bypass grant -- copied from the
// shape reported against erun's own merge-queue pushes, including the blank
// remote lines GitHub separates the listed rules with.
const gitHubBypassNotice = "remote: Bypassed rule violations for refs/heads/main:\n" +
	"remote: \n" +
	"remote: - Changes must be made through a pull request.\n" +
	"remote: \n"

// TestPushWorkingTreeBranchReportsAGitHubRulesetBypass reproduces the blind
// spot this exists to close: the push succeeds, GitHub says on stderr that it
// was admitted by a bypass rather than by the rules, and erun reported nothing
// at all. The push captured stderr into a buffer to interpolate it into a
// failure message and discarded that buffer on success, so a bypassed push and
// a compliant one read identically in erun's own output -- and GitHub's remote
// line, the only other trace, was captured too.
//
// Driven through the RunGit seam rather than a real remote: no real repository
// can be made to answer a push with a ruleset bypass, so the seam is the only
// way to hold the report to account.
func TestPushWorkingTreeBranchReportsAGitHubRulesetBypass(t *testing.T) {
	var out bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &out, &out)}
	deps := PushWorkingTreeBranchDependencies{
		CurrentBranch: func(Context, string) (string, error) { return "main", nil },
		CurrentCommit: func(Context, string) (string, error) { return "abc1234", nil },
		RunGit: func(_ string, _ io.Writer, stderr io.Writer, args ...string) error {
			if _, err := fmt.Fprint(stderr, gitHubBypassNotice); err != nil {
				t.Fatalf("writing the stub push's stderr: %v", err)
			}
			// A bypass is not a failure: GitHub admits the push.
			return nil
		},
	}

	result, err := PushWorkingTreeBranch(ctx, t.TempDir(), PushWorkingTreeBranchParams{Branch: "main"}, deps)
	if err != nil {
		t.Fatalf("a bypassed push still succeeds, so it must not be reported as an error: %v", err)
	}
	if result.Commit != "abc1234" {
		t.Fatalf("expected the pushed commit to be read back, got %q", result.Commit)
	}

	got := out.String()
	for _, want := range []string{"refs/heads/main", "Changes must be made through a pull request", "reconcile-bypass"} {
		if !strings.Contains(got, want) {
			t.Errorf("a bypassed push must say so in erun's own output, naming %q; output was:\n%s", want, got)
		}
	}
}

// TestPushWorkingTreeBranchStaysSilentWhenNoRuleWasBypassed is the other half:
// the report is keyed on GitHub actually saying a rule was bypassed, so every
// ordinary push stays as quiet as it was.
func TestPushWorkingTreeBranchStaysSilentWhenNoRuleWasBypassed(t *testing.T) {
	var out bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &out, &out)}
	deps := PushWorkingTreeBranchDependencies{
		CurrentBranch: func(Context, string) (string, error) { return "feature/widget", nil },
		CurrentCommit: func(Context, string) (string, error) { return "abc1234", nil },
		RunGit: func(_ string, _ io.Writer, stderr io.Writer, args ...string) error {
			if _, err := fmt.Fprint(stderr, "To github.com:sophium/erun.git\n   abc1234..def5678  feature/widget -> feature/widget\n"); err != nil {
				t.Fatalf("writing the stub push's stderr: %v", err)
			}
			return nil
		},
	}

	if _, err := PushWorkingTreeBranch(ctx, t.TempDir(), PushWorkingTreeBranchParams{Branch: "feature/widget"}, deps); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := out.String(); strings.Contains(got, "bypassing") {
		t.Errorf("an ordinary push must not be reported as a bypass, got:\n%s", got)
	}
}

// tagBypassNotice is the same notice as it appears on a tag push, so a test
// that drives the tag path proves which push reported rather than only that
// something did.
const tagBypassNotice = "remote: Bypassed rule violations for refs/tags/v1.4.2:\n" +
	"remote: \n" +
	"remote: - Changes must be made through a pull request.\n" +
	"remote: \n"

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
func releaseBypassTestContext(out io.Writer) Context {
	return Context{
		Logger: NewLoggerWithWriters(VerbosityInfo, out, out),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

// releaseBypassPushRunner stands in for the push: it answers with the given
// stderr and lets the push through, which is what GitHub does when a credential
// holding a bypass grant steps over the rules.
func releaseBypassPushRunner(stderrText string) GitCommandRunnerFunc {
	return func(_ string, _ io.Writer, stderr io.Writer, _ ...string) error {
		if _, err := fmt.Fprint(stderr, stderrText); err != nil {
			return err
		}
		return nil
	}
}

// TestReleaseReportsAGitHubRulesetBypassOnItsBranchPush reproduces the half of
// the reported defect the working-tree primitive's own fix did not reach: the
// release makes pushes of its own -- the `--follow-tags` push that makes its
// generated commits public, and the tag push that publishes the version -- and
// neither read the notice. A release whose push was admitted by a bypass
// reported it exactly as one whose push satisfied every rule, so the single
// operation that writes a protected branch was silent about writing through it
// rather than around it, and silent precisely when the credential in use held
// the grant.
//
// Driven through the real stage constructor and the RunGit seam: the notice
// only ever comes from a live remote, so the seam is the only way to hold the
// report to account, and the stage is what pins the arguments reported on.
func TestReleaseReportsAGitHubRulesetBypassOnItsBranchPush(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := newPushReleaseStage(t.TempDir(), ReleaseConfig{MainBranch: "main", DevelopBranch: "develop"}, false)

	err := runReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(gitHubBypassNotice))
	if err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), "refs/heads/main")
}

// TestReleaseReportsAGitHubRulesetBypassOnTheVersionTagPush is the tag half.
// The tag push is a stage command rather than the release's branch push, so it
// reaches the report by the other route; without that route it is the one push
// that publishes a version and says nothing about how it landed.
func TestReleaseReportsAGitHubRulesetBypassOnTheVersionTagPush(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := newPushReleaseTagStage(t.TempDir(), "1.4.2")

	err := runReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(tagBypassNotice))
	if err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), "refs/tags/v1.4.2")
}

// TestReleasePushStaysSilentWhenNoRuleWasBypassed is the other direction for
// this path: the report is keyed on GitHub actually saying a rule was bypassed,
// so an ordinary release push stays as quiet as it was.
func TestReleasePushStaysSilentWhenNoRuleWasBypassed(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := newPushReleaseStage(t.TempDir(), ReleaseConfig{MainBranch: "main", DevelopBranch: "develop"}, false)

	ordinary := "To github.com:sophium/erun.git\n   46e01916..ae29151f  main -> main\n"
	if err := runReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(ordinary)); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := out.String(); strings.Contains(got, "bypassing") {
		t.Errorf("an ordinary release push must not be reported as a bypass, got:\n%s", got)
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
	ref := releaseRepoClaimRef("1.4.2")
	installBypassNoticeHook(t, remote, ref)

	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)

	sha, err := Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("resolve the commit the claim push names: %v", err)
	}
	if err := gitPushCreateRef(ctx, repo, "origin", strings.TrimSpace(string(sha)), ref); err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), ref)
}

func TestParseRulesetBypassNotice(t *testing.T) {
	tests := []struct {
		name      string
		stderr    string
		wantOK    bool
		wantRef   string
		wantRules []string
	}{
		{
			name:      "the reported shape, with the ref and one rule",
			stderr:    gitHubBypassNotice,
			wantOK:    true,
			wantRef:   "refs/heads/main",
			wantRules: []string{"Changes must be made through a pull request."},
		},
		{
			name:   "a notice naming no ref still reports the rules",
			stderr: "remote: Bypassed rule violations:\nremote: - Required status check \"build\" is expected.\n",
			wantOK: true,
			wantRules: []string{
				`Required status check "build" is expected.`,
			},
		},
		{
			name:      "several rules, separated by blank remote lines",
			stderr:    "remote: Bypassed rule violations for refs/heads/release:\nremote: \nremote: - Changes must be made through a pull request.\nremote: \nremote: - Required linear history.\n",
			wantOK:    true,
			wantRef:   "refs/heads/release",
			wantRules: []string{"Changes must be made through a pull request.", "Required linear history."},
		},
		{
			name:   "unrelated remote output is not a notice",
			stderr: "remote: Resolving deltas: 100% (3/3), completed with 3 local objects.\nremote: \nTo github.com:sophium/erun.git\n",
		},
		{
			// A notice with no rule list under it still reports, with an empty
			// list: GitHub saying a bypass happened is the fact that matters,
			// and a ruleset that lists nothing still bypassed something.
			name:    "a notice with no rules listed still reports",
			stderr:  "remote: Bypassed rule violations for refs/heads/main:\n",
			wantOK:  true,
			wantRef: "refs/heads/main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice, ok := parseRulesetBypassNotice(tt.stderr)
			if ok != tt.wantOK {
				t.Fatalf("parseRulesetBypassNotice(%q) ok = %v, want %v", tt.stderr, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if notice.Ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", notice.Ref, tt.wantRef)
			}
			if strings.Join(notice.Rules, "|") != strings.Join(tt.wantRules, "|") {
				t.Errorf("rules = %q, want %q", notice.Rules, tt.wantRules)
			}
		})
	}
}
