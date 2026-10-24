package build

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseflow"
)

// gitHubBypassNotice is copied from a real push, including the blank remote lines.
const gitHubBypassNotice = "remote: Bypassed rule violations for refs/heads/main:\n" +
	"remote: \n" +
	"remote: - Changes must be made through a pull request.\n" +
	"remote: \n"

const tagBypassNotice = "remote: Bypassed rule violations for refs/tags/v1.4.2:\n" +
	"remote: \n" +
	"remote: - Changes must be made through a pull request.\n" +
	"remote: \n"

func assertBypassReported(t *testing.T, got, wantRef string) {
	t.Helper()
	for _, want := range []string{"origin", wantRef, "Changes must be made through a pull request", "reconcile-bypass"} {
		if !strings.Contains(got, want) {
			t.Errorf("a push admitted by a ruleset bypass must say so in erun's own output, naming %q; output was:\n%s", want, got)
		}
	}
}

// releaseBypassTestContext gives git a real stderr sink, as production does.
func releaseBypassTestContext(out io.Writer) core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, out, out),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func releaseBypassPushRunner(stderrText string) gitx.GitCommandRunnerFunc {
	return func(_ string, _ io.Writer, stderr io.Writer, _ ...string) error {
		if _, err := fmt.Fprint(stderr, stderrText); err != nil {
			return err
		}
		return nil
	}
}

func TestReleaseReportsAGitHubRulesetBypassOnItsBranchPush(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := NewPushReleaseStage(t.TempDir(), config.ReleaseConfig{MainBranch: "main", DevelopBranch: "develop"}, false)

	err := releaseflow.RunReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(gitHubBypassNotice))
	if err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), "refs/heads/main")
}

// The tag push is a stage command, so it reaches the report by another route.
func TestReleaseReportsAGitHubRulesetBypassOnTheVersionTagPush(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := NewPushReleaseTagStage(t.TempDir(), "1.4.2")

	err := releaseflow.RunReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(tagBypassNotice))
	if err != nil {
		t.Fatalf("a bypass is not a failure, so it must not be reported as an error: %v", err)
	}
	assertBypassReported(t, out.String(), "refs/tags/v1.4.2")
}

func TestReleasePushStaysSilentWhenNoRuleWasBypassed(t *testing.T) {
	var out bytes.Buffer
	ctx := releaseBypassTestContext(&out)
	stage := NewPushReleaseStage(t.TempDir(), config.ReleaseConfig{MainBranch: "main", DevelopBranch: "develop"}, false)

	ordinary := "To github.com:sophium/erun.git\n   46e01916..ae29151f  main -> main\n"
	if err := releaseflow.RunReleaseCommand(ctx, releasePushTestSpec(), stage, stage.GitCommands[0], releaseBypassPushRunner(ordinary)); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := out.String(); strings.Contains(got, "bypassing") {
		t.Errorf("an ordinary release push must not be reported as a bypass, got:\n%s", got)
	}
}
