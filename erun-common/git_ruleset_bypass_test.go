package eruncommon

import (
	"bytes"
	"fmt"
	"io"
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
