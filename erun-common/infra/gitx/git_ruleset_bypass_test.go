package gitx

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// gitHubBypassNotice is the real stderr shape, including blank separator lines.
const gitHubBypassNotice = "remote: Bypassed rule violations for refs/heads/main:\n" +
	"remote: \n" +
	"remote: - Changes must be made through a pull request.\n" +
	"remote: \n"

// No real remote can produce a bypass, so this drives the RunGit seam.
func TestPushWorkingTreeBranchReportsAGitHubRulesetBypass(t *testing.T) {
	var out bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &out, &out)}
	deps := PushWorkingTreeBranchDependencies{
		CurrentBranch: func(core.Context, string) (string, error) { return "main", nil },
		CurrentCommit: func(core.Context, string) (string, error) { return "abc1234", nil },
		RunGit: func(_ string, _ io.Writer, stderr io.Writer, args ...string) error {
			if _, err := fmt.Fprint(stderr, gitHubBypassNotice); err != nil {
				t.Fatalf("writing the stub push's stderr: %v", err)
			}
			return nil
		},
	}

	result, err := PushWorkingTreeBranch(ctx, t.TempDir(), model.PushWorkingTreeBranchParams{Branch: "main"}, deps)
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

func TestPushWorkingTreeBranchStaysSilentWhenNoRuleWasBypassed(t *testing.T) {
	var out bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &out, &out)}
	deps := PushWorkingTreeBranchDependencies{
		CurrentBranch: func(core.Context, string) (string, error) { return "feature/widget", nil },
		CurrentCommit: func(core.Context, string) (string, error) { return "abc1234", nil },
		RunGit: func(_ string, _ io.Writer, stderr io.Writer, args ...string) error {
			if _, err := fmt.Fprint(stderr, "To github.com:sophium/erun.git\n   abc1234..def5678  feature/widget -> feature/widget\n"); err != nil {
				t.Fatalf("writing the stub push's stderr: %v", err)
			}
			return nil
		},
	}

	if _, err := PushWorkingTreeBranch(ctx, t.TempDir(), model.PushWorkingTreeBranchParams{Branch: "feature/widget"}, deps); err != nil {
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
