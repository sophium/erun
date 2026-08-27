package main

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// TestRuntimeSizingFromOutputParsesActions covers the happy path: the resize
// command's own "resize: tenant/env resource from -> to" trace lines (printed
// at default verbosity via ctx.Trace) are what the Runtime tab's sizing
// preview and apply calls parse out of the combined kubectl-exec output,
// since the JSON output would otherwise interleave unpredictably with trace
// text once both share one captured stream.
func TestRuntimeSizingFromOutputParsesActions(t *testing.T) {
	output := "resize: myapp/prod cpu 4 -> 6\n" +
		"resize: myapp/prod memory 8916Mi -> 12288Mi\n" +
		"resize: myapp/prod this moves the runtime container's throttle/OOM ceiling and its namespace quota draw; it does not change what the scheduler reserves (a fixed request independent of runtimepod) and it does not resize the erun-dind sidecar or any PVC\n" +
		"resize: myapp/prod will roll the runtime pod once to apply the new limits\n" +
		"cpu: 4 -> 6\n" +
		"memory: 8916Mi -> 12288Mi\n" +
		"==> Resized myapp/prod\n"

	got := runtimeSizingFromOutput(uiSelection{Tenant: "myapp", Environment: "prod"}, output)

	want := uiRuntimeSizingRecommendation{
		Tenant:      "myapp",
		Environment: "prod",
		Available:   true,
		Actions: []uiRuntimeSizingAction{
			{Resource: "cpu", From: "4", To: "6"},
			{Resource: "memory", From: "8916Mi", To: "12288Mi"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected recommendation:\n got %+v\nwant %+v", got, want)
	}
}

// TestRuntimeSizingFromOutputParsesNoOp covers the case that must NOT deploy:
// the resolved recommendation already matches the current size.
func TestRuntimeSizingFromOutputParsesNoOp(t *testing.T) {
	output := "resize: myapp/prod is already sized at cpu=4 memory=8916Mi; no change\n"

	got := runtimeSizingFromOutput(uiSelection{Tenant: "myapp", Environment: "prod"}, output)

	if !got.Available || !got.NoOp || len(got.Actions) != 0 {
		t.Fatalf("expected an available no-op with no actions, got %+v", got)
	}
}

// TestRuntimeSizingFromOutputHandlesUnrecognisedOutput covers a version drift
// or a truly empty successful run: no known line survives, so the tab reports
// "nothing available" rather than a blank success.
func TestRuntimeSizingFromOutputHandlesUnrecognisedOutput(t *testing.T) {
	got := runtimeSizingFromOutput(uiSelection{Tenant: "myapp", Environment: "prod"}, "some unrelated line\n")

	if got.Available {
		t.Fatalf("expected unavailable when no known line is present, got %+v", got)
	}
	if got.Message == "" {
		t.Fatalf("expected a message explaining the empty result")
	}
}

func TestShellSingleQuoteEscapesEmbeddedQuotes(t *testing.T) {
	got := shellSingleQuote("o'brien")
	want := `'o'\''brien'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFriendlyRuntimeResizeErrorStripsWrapper(t *testing.T) {
	// Mirrors kubectlText's fmt.Errorf("%w: %s", err, text) wrapping: the
	// "exit status 1: " prefix lands only on the first line of the pod's own
	// multi-line combined output, with the trace lines printed before the
	// refusal preserved on their own lines above it.
	wrapped := &wrappedError{msg: "exit status 1: resize: myapp/prod cpu 4 -> 6\n" +
		"resize refused: this environment is held by user jane"}
	got := friendlyRuntimeResizeError(wrapped)
	if got != "this environment is held by user jane" {
		t.Fatalf("got %q", got)
	}
}

type wrappedError struct{ msg string }

func (e *wrappedError) Error() string { return e.msg }

// TestLoadRuntimeSizingReportsOwnTimeoutNotSignalKilled is the reported defect
// for the sizing panel: with the app's own context already past its deadline,
// LoadRuntimeSizing's derived ctx reads as already timed out, so the panel
// must name that timeout rather than the "signal: killed" text the mocked
// exec below deliberately returns.
func TestLoadRuntimeSizingReportsOwnTimeoutNotSignalKilled(t *testing.T) {
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uiSelection, string) (string, error) {
			return "", errors.New("signal: killed")
		},
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	app.ctx = ctx

	recommendation, err := app.LoadRuntimeSizing(uiSelection{Tenant: "myapp", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadRuntimeSizing must not surface a probe failure as an error: %v", err)
	}
	if recommendation.Available {
		t.Fatalf("a timed-out probe must not be reported as an available reading: %+v", recommendation)
	}
	if !strings.Contains(recommendation.Message, "timed out") {
		t.Fatalf("expected the panel to name its own timeout, got %q", recommendation.Message)
	}
	if strings.Contains(recommendation.Message, "signal:") {
		t.Fatalf("the panel must never repeat the raw kill signal on a timeout, got %q", recommendation.Message)
	}
}

// TestLoadRuntimeSizingReportsExternalKillDistinctFromTimeout covers a kill
// this probe did not cause, which must read differently from the
// self-inflicted timeout above and must never name the bare signal either.
func TestLoadRuntimeSizingReportsExternalKillDistinctFromTimeout(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("signal-terminated exec is a POSIX concept")
	}
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uiSelection, string) (string, error) {
			cmd := exec.Command("/bin/sh", "-c", "kill -9 $$")
			return "", cmd.Run()
		},
	})
	app.ctx = context.Background()

	recommendation, err := app.LoadRuntimeSizing(uiSelection{Tenant: "myapp", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadRuntimeSizing must not surface a probe failure as an error: %v", err)
	}
	if strings.Contains(recommendation.Message, "timed out") {
		t.Fatalf("a kill this probe did not cause must not be read as its own timeout, got %q", recommendation.Message)
	}
	if strings.Contains(recommendation.Message, "signal:") {
		t.Fatalf("the panel must never repeat the raw kill signal, got %q", recommendation.Message)
	}
	if !strings.Contains(recommendation.Message, "killed") {
		t.Fatalf("expected the panel to name an external kill, got %q", recommendation.Message)
	}
}
