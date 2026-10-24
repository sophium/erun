package mergequeue

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// gateMergeTrailerSeam fakes one squash's git calls and records the committed message.
func gateMergeTrailerSeam(body string, committed *string) GateMergeWorkingTreeDependencies {
	return GateMergeWorkingTreeDependencies{
		ResolveRef: func(core.Context, string, string) (string, error) { return "abc123", nil },
		RunGit: func(_ string, stdout io.Writer, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "log" {
				_, _ = fmt.Fprint(stdout, body+"\x00")
			}
			if len(args) >= 3 && args[0] == "commit" {
				*committed = args[len(args)-1]
			}
			return nil
		},
		StagedPaths: func(core.Context, string) ([]string, error) { return []string{"widget.go"}, nil },
	}
}

func TestGateMergeOneSourceCarriesTheSourceBranchesTrailers(t *testing.T) {
	const branchBody = "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n\n" +
		"Closes #2601\n" +
		"Reproduces: a caller reading the widget got its parts in the order they\n" +
		"  were appended rather than the order they were declared.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n"
	var committed string
	deps := gateMergeTrailerSeam(branchBody, &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "feature", Message: "Assemble the widget in declared order"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}

	if committed == "" {
		t.Fatal("expected the squash to be committed with a message")
	}
	if first := strings.SplitN(committed, "\n", 2)[0]; first != "Assemble the widget in declared order" {
		t.Fatalf("the squash commit must still lead with the review name, got %q", first)
	}
	for _, want := range []string{
		"Closes #2601",
		"Reproduces: a caller reading the widget got its parts in the order they\n  were appended rather than the order they were declared.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	} {
		if !strings.Contains(committed, want) {
			t.Fatalf("the squash commit must carry the branch's own %q trailer, got:\n%s", want, committed)
		}
	}
	if strings.Contains(committed, "assembled backwards") {
		t.Fatalf("only the branch's trailers belong beneath the review name, not its prose, got:\n%s", committed)
	}
}

func TestGateMergeTrailersFromBodyStopsAHardWrappedValueAtItsParagraph(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Reproduces: the parts arrived in append order rather than the order they\n" +
		"were declared, so every caller reading the widget got\n" +
		"  the parts back to front.\n" +
		"\n" +
		"The widget was assembled backwards by the old builder.\n" +
		"\n" +
		"Closes #2703\n" +
		"Note: this is a workaround for a quirk\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{
		"Reproduces: the parts arrived in append order rather than the order they\nwere declared, so every caller reading the widget got\n  the parts back to front.",
		"Closes #2703",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trailer %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

// TestGateMergeTrailersFromBodyStopsAClosingReferenceAtItsOwnLine: "Closes #N" has no continuation,
// so prose on the next line is not carried.
func TestGateMergeTrailersFromBodyStopsAClosingReferenceAtItsOwnLine(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Closes #2703\n" +
		"See the linked issue for the full reproduction, which I ran by hand.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{
		"Closes #2703",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trailer %d: expected %q, got %q", i, want[i], got[i])
		}
	}
	if strings.Contains(got[0], "See the linked issue") {
		t.Fatalf("the prose under the declaration must not be carried as its value, got %q", got[0])
	}
}

func TestGateMergeTrailersFromBodyStopsAContinuableValueAtAnUnrecognisedTrailer(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Reproduces: the parts arrived in append order rather than the order they\n" +
		"were declared.\n" +
		"Signed-off-by: A Author <a@example.com>\n" +
		"Refs #2642\n" +
		"Defect-Fix: yes\n" +
		"the line under a single-line trailer is not its value\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{
		"Reproduces: the parts arrived in append order rather than the order they\nwere declared.",
		"Defect-Fix: yes",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trailer %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

// TestGateMergeTrailersFromBodyReadsAColumnZeroTokenLineAsANewEntry records a known residual: a
// column-0 "Token:" continuation is indistinguishable from a new entry.
func TestGateMergeTrailersFromBodyReadsAColumnZeroTokenLineAsANewEntry(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Reproduces: the card closed under a stationary pointer, and\n" +
		"Note: the observer had already been detached by the boot's landing.\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{"Reproduces: the card closed under a stationary pointer, and"}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	if got[0] != want[0] {
		t.Fatalf("expected the value to stop at the trailer-shaped continuation, got %q", got[0])
	}
}

func TestGateMergeTrailersFromBodyReadsEveryDeclaredTrailer(t *testing.T) {
	body := "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n\n" +
		"Closes #2642\n" +
		"\n" +
		"Reproduces: the parts arrived in append order.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n" +
		"\n" +
		"Refs #2642\n" +
		"Note: this is a workaround for a quirk\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{
		"Closes #2642",
		"Reproduces: the parts arrived in append order.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trailer %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestGateMergeTrailersFromBodyIsEmptyWhenTheBodyDeclaresNoTrailers(t *testing.T) {
	body := "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n"
	if got := gateMergeTrailersFromBody(body); len(got) != 0 {
		t.Fatalf("expected no carried trailers, got %q", got)
	}
}

func TestGateMergeOneSourceDoesNotRepeatTrailersTheCallerAlreadyPassed(t *testing.T) {
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\n", &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "feature", Message: "Fix the widget\n\nCloses #2601"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}
	if got := strings.Count(committed, "Closes #2601"); got != 1 {
		t.Fatalf("expected the trailer once, got %d in:\n%s", got, committed)
	}
}

func TestGateMergeOneSourceIgnoresTrailersThatAreNotLoadBearing(t *testing.T) {
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\nNote: this is a workaround for a quirk\n", &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "feature", Message: "Fix the widget"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}
	if !strings.Contains(committed, "Closes #2601") {
		t.Fatalf("expected the issue-closing trailer to be carried, got:\n%s", committed)
	}
	if strings.Contains(committed, "Note: this is a workaround") {
		t.Fatalf("expected an unrecognised trailing line to stay behind, got:\n%s", committed)
	}
}

func TestGateMergeOneSourceSkipsADefectFixThatNamesNoReproduction(t *testing.T) {
	var gitCalls []string
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\n", &committed)
	runGit := deps.RunGit
	deps.RunGit = func(root string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, strings.Join(args, " "))
		return runGit(root, stdout, stderr, args...)
	}

	landed, skipped, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "bug/2601-widget", Message: "Fix the widget"}, "origin", "refs/erun/gate-merge/main", deps)
	if err != nil {
		t.Fatalf("a policy refusal must be a skip, not a batch failure: %v", err)
	}
	if landed != nil || skipped == nil {
		t.Fatalf("expected the source to be skipped, got landed=%+v skipped=%+v", landed, skipped)
	}
	if !strings.HasPrefix(skipped.Reason, "regression-test policy: ") || !strings.Contains(skipped.Reason, `"Regression-Test:"`) {
		t.Fatalf("expected the skip to name the missing declaration, got %q", skipped.Reason)
	}
	if !slices.Contains(gitCalls, "reset --hard HEAD") {
		t.Fatalf("expected the squash to be backed out, got git calls %q", gitCalls)
	}
	if committed != "" {
		t.Fatalf("a refused source must not be committed, got %q", committed)
	}
}
