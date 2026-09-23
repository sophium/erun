package eruncommon

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// gateMergeTrailerSeam answers the git calls one squash makes: the branch's
// own commits read back as body, one path appears staged so the source is a
// real contribution, and the message the squash is committed with is recorded
// in *committed. The carriage is asserted through that message rather than
// through a real repository, because the message is what the commit is made
// from and this seam holds that choice to account directly.
func gateMergeTrailerSeam(body string, committed *string) GateMergeWorkingTreeDependencies {
	return GateMergeWorkingTreeDependencies{
		ResolveRef: func(Context, string, string) (string, error) { return "abc123", nil },
		RunGit: func(_ string, stdout io.Writer, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "log" {
				_, _ = fmt.Fprint(stdout, body+"\x00")
			}
			if len(args) >= 3 && args[0] == "commit" {
				*committed = args[2]
			}
			return nil
		},
		StagedPaths: func(Context, string) ([]string, error) { return []string{"widget.go"}, nil },
	}
}

// TestGateMergeOneSourceCarriesTheSourceBranchesTrailers is the reproduction
// of the reported failure: a branch whose own commits declare "Closes #N"
// landed through the merge queue with that declaration discarded, because the
// squash commit's message was only ever the caller-supplied review name. The
// issue stayed open with its fix already on the target, and no commit on the
// target referenced it — so a later reader had no way to tell a fixed issue
// from an open one.
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

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Assemble the widget in declared order"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
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

// TestGateMergeOneSourceCarriesTrailersSeparatedFromACoAuthoredBy is the
// reproduction of the reported failure: a branch whose authored trailers are
// separated from a trailing "Co-Authored-By:" by a blank line — the shape git
// itself produces, and the one five sources of a real landing carried — lost
// every one of them. gateMergeTrailerBlock read only the final run of
// non-empty lines, and that run was the Co-Authored-By: alone, so the authored
// "Closes #N" and three reproduction trailers were dropped from the squash.
// The issue stayed open and the commit that landed asserted no reproduction for
// the defect it fixed, with nothing warning that anything had been discarded.
func TestGateMergeOneSourceCarriesTrailersSeparatedFromACoAuthoredBy(t *testing.T) {
	const branchBody = "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n\n" +
		"Closes #2642\n" +
		"Reproduces: a caller reading the widget got its parts in the order they\n" +
		"  were appended rather than the order they were declared.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n" +
		"\n" +
		"Co-Authored-By: Claude <noreply@anthropic.com>\n"
	var committed string
	deps := gateMergeTrailerSeam(branchBody, &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Assemble the widget in declared order"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}

	for _, want := range []string{
		"Closes #2642",
		"Reproduces: a caller reading the widget got its parts in the order they\n  were appended rather than the order they were declared.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	} {
		if !strings.Contains(committed, want) {
			t.Fatalf("the squash commit must carry the branch's own %q trailer across the Co-Authored-By: line, got:\n%s", want, committed)
		}
	}
	if strings.Contains(committed, "assembled backwards") {
		t.Fatalf("only the branch's trailers belong beneath the review name, not its prose, got:\n%s", committed)
	}
	// The Co-Authored-By: is a trailer-shaped line like the rest of the block,
	// not a boundary that replaces it; it is simply not one this squash carries.
	if strings.Contains(committed, "Co-Authored-By:") {
		t.Fatalf("the squash carries only the load-bearing trailers, got:\n%s", committed)
	}
}

// TestGateMergeTrailerBlockIsEmptyWhenTheBodyDeclaresNoTrailers pins the
// ordinary case the collapse must not regress: a message whose trailing
// paragraph is prose yields no block, so nothing is read as a trailer that was
// never meant as one.
func TestGateMergeTrailerBlockIsEmptyWhenTheBodyDeclaresNoTrailers(t *testing.T) {
	body := "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n"
	if got := gateMergeTrailerBlock(body); len(got) != 0 {
		t.Fatalf("expected no trailer block, got %q", got)
	}
	if got := gateMergeTrailersFromBody(body); len(got) != 0 {
		t.Fatalf("expected no carried trailers, got %q", got)
	}
}

// TestGateMergeTrailerBlockSpansABlankLineBetweenTrailerParagraphs covers the
// other half of the collapse: git permits a blank line between two trailer
// paragraphs, and the block has to span it rather than keep only the paragraph
// closest to the end.
func TestGateMergeTrailerBlockSpansABlankLineBetweenTrailerParagraphs(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Closes #2642\n" +
		"\n" +
		"Reproduces: the parts arrived in append order.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{
		"Closes #2642",
		"Reproduces: the parts arrived in append order.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d trailers across the blank line, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trailer %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

// TestGateMergeOneSourceDoesNotRepeatTrailersTheCallerAlreadyPassed covers the
// other half of the same contract: a caller that supplies the branch's own
// message verbatim — which is how the one squash known to carry trailers got
// them — must not end up with every trailer twice.
func TestGateMergeOneSourceDoesNotRepeatTrailersTheCallerAlreadyPassed(t *testing.T) {
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\n", &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Fix the widget\n\nCloses #2601"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}
	if got := strings.Count(committed, "Closes #2601"); got != 1 {
		t.Fatalf("expected the trailer once, got %d in:\n%s", got, committed)
	}
}

// TestGateMergeOneSourceIgnoresTrailersThatAreNotLoadBearing pins the closed
// set: the carriage exists to preserve the lines this repository's guidance
// makes load-bearing for a landed commit, not to copy arbitrary trailing prose
// from a branch onto an unattended commit on the target.
func TestGateMergeOneSourceIgnoresTrailersThatAreNotLoadBearing(t *testing.T) {
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\nNote: this is a workaround for a quirk\n", &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Fix the widget"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}
	if !strings.Contains(committed, "Closes #2601") {
		t.Fatalf("expected the issue-closing trailer to be carried, got:\n%s", committed)
	}
	if strings.Contains(committed, "Note: this is a workaround") {
		t.Fatalf("expected an unrecognised trailing line to stay behind, got:\n%s", committed)
	}
}
