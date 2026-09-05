package eruncommon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The failure this locks: a detached gate's only durable artefact is its timing
// record, and for every one of the builds that prompted this the record said
// "exit status 1" while the reason lived solely in a stream nobody kept.
func TestDockerBuildFailureReasonKeepsTheFailingStepsOwnWords(t *testing.T) {
	output := `#5 [2/3] RUN echo hello
#5 0.187 hello
#5 DONE 0.2s
#6 [3/3] RUN curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-v4.2.2-linux-amd64.tar.gz"
#6 0.208 fetching helm
#6 31.56 curl: (28) Connection timed out after 30002 milliseconds
#6 ERROR: process "/bin/sh -c curl -fsSL ..." did not complete successfully: exit code: 28
ERROR: failed to solve: process "/bin/sh -c curl -fsSL ..." did not complete successfully: exit code: 28`

	reason := dockerBuildFailureReason(output)
	if !strings.Contains(reason, "curl: (28) Connection timed out after 30002 milliseconds") {
		t.Fatalf("the reason should carry the failing step's own output, got: %q", reason)
	}
	if !strings.Contains(reason, "exit code: 28") {
		t.Fatalf("the reason should also carry BuildKit's headline, got: %q", reason)
	}
	if strings.Contains(reason, "hello") {
		t.Fatalf("the reason should not carry an unrelated successful step's output, got: %q", reason)
	}
}

func TestDockerBuildFailureReasonIsBounded(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < 40; i++ {
		builder.WriteString("#6 1.00 " + strings.Repeat("x", 80) + "\n")
	}
	builder.WriteString(`#6 ERROR: process "/bin/sh -c thing" did not complete successfully: exit code: 1`)

	reason := dockerBuildFailureReason(builder.String())
	if reason == "" {
		t.Fatal("a bounded reason should still be a reason")
	}
	if len(reason) > buildFailureReasonMaxLength+len("…") {
		t.Fatalf("the reason should be bounded for a record meant to be skimmed, got %d chars", len(reason))
	}
}

func TestDockerBuildFailureReasonFallsBackToTheLastErrorLine(t *testing.T) {
	// A daemon that refused before any step ran emits no per-step frame at all.
	output := `ERROR: failed to solve: failed to resolve source metadata for docker.io/library/nope:1: not found`

	reason := dockerBuildFailureReason(output)
	if !strings.Contains(reason, "failed to resolve source metadata") {
		t.Fatalf("expected the unframed ERROR line as a fallback, got: %q", reason)
	}
}

func TestDockerBuildFailureReasonIsEmptyWhenNothingIsRecognisable(t *testing.T) {
	if reason := dockerBuildFailureReason("just some chatter\nand more chatter\n"); reason != "" {
		t.Fatalf("unrecognisable output should invent no reason, got: %q", reason)
	}
}

func TestDockerBuildStepErrorReportsTheReasonAndKeepsTheProcessError(t *testing.T) {
	underlying := errors.New("exit status 1")
	err := DockerBuildStepError{Reason: "curl: (35) Recv failure", Err: underlying}

	if err.Error() != "curl: (35) Recv failure" {
		t.Fatalf("the error should read as the reason, got: %q", err.Error())
	}
	if !errors.Is(err, underlying) {
		t.Fatal("the underlying process error should stay reachable for exit-code matching")
	}
}

func TestDockerBuildStepErrorFallsBackToTheProcessErrorWithNoReason(t *testing.T) {
	err := DockerBuildStepError{Err: errors.New("exit status 1")}
	if err.Error() != "exit status 1" {
		t.Fatalf("with no reason the process error should show through, got: %q", err.Error())
	}
}

func TestJoinFailureReasonKeepsBothTheStepAndTheDiagnosis(t *testing.T) {
	joined := joinFailureReason("curl: (35) Recv failure", "bridged at MTU 1500 but the pod carries 1450")
	if !strings.Contains(joined, "curl: (35) Recv failure") || !strings.Contains(joined, "MTU 1500") {
		t.Fatalf("a diagnosis should be added to the step's words, not replace them, got: %q", joined)
	}
	if got := joinFailureReason("", "only a diagnosis"); got != "only a diagnosis" {
		t.Fatalf("expected the diagnosis alone, got: %q", got)
	}
	if got := joinFailureReason("only a reason", ""); got != "only a reason" {
		t.Fatalf("expected the reason alone, got: %q", got)
	}
}

// Closes the chain that was broken end to end: BuildKit's output becomes a
// reason, the reason becomes the error a failed step carries, and that error is
// what the durable timing record persists. Before this, every link past the
// first dropped everything but "exit status 1".
func TestBuildFailureReasonReachesTheDurableTimingRecord(t *testing.T) {
	output := `#6 [3/3] RUN curl -fsSL -o /tmp/helm.tar.gz "https://get.helm.sh/helm-v4.2.2-linux-amd64.tar.gz"
#6 31.56 curl: (35) Recv failure: Connection reset by peer
#6 ERROR: process "/bin/sh -c curl -fsSL ..." did not complete successfully: exit code: 35
ERROR: failed to solve: process "/bin/sh -c curl -fsSL ..." did not complete successfully: exit code: 35`

	buildErr := DockerBuildStepError{
		Reason: dockerBuildFailureReason(output),
		Err:    errors.New("exit status 1"),
	}

	clock := newFakeClock()
	root := newStepTiming("build", clock.now)
	image := root.child("erun-devops")
	platform := image.child("linux/amd64")
	clock.advance(2 * time.Minute)
	platform.finish(buildErr)
	image.finish(buildErr)
	root.finish(buildErr)

	record := root.toRecord("build")
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal timing record: %v", err)
	}
	if strings.Contains(string(encoded), `"error":"exit status 1"`) {
		t.Fatalf("the record should not fall back to the bare exit status:\n%s", encoded)
	}
	for _, want := range []string{"Recv failure", "Connection reset by peer", "exit code: 35"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("the durable record should carry %q, got:\n%s", want, encoded)
		}
	}
	if !strings.Contains(record.Steps[0].Steps[0].Error, "Recv failure") {
		t.Fatalf("the failing platform step should carry the reason too, got: %q", record.Steps[0].Steps[0].Error)
	}
}
