package main

import (
	"reflect"
	"testing"
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
