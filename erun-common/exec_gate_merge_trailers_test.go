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

// TestGateMergeOneSourceCarriesTrailersWhenTheBranchEndsWithAnIssueReference is
// the reproduction of the reported failure: a branch whose authored trailers
// close with a bare "Refs #N" — a line at the start of the closing paragraph
// with no colon, so not a "Token:" line — landed through the merge queue with
// every declaration above it discarded. The block walk stopped at the
// unrecognised paragraph, the block came back empty, and the squash carried
// only the caller's message: the issue stayed open with its fix on the target,
// and the commit asserted no reproduction for the defect it fixed, while the
// gate-merge reported success either way.
func TestGateMergeOneSourceCarriesTrailersWhenTheBranchEndsWithAnIssueReference(t *testing.T) {
	const branchBody = "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n\n" +
		"Closes #2662\n" +
		"Reproduces: a caller reading the widget got its parts in the order they\n" +
		"  were appended rather than the order they were declared.\n" +
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n" +
		"\n" +
		"Refs #2662\n"
	var committed string
	deps := gateMergeTrailerSeam(branchBody, &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Assemble the widget in declared order"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}

	for _, want := range []string{
		"Closes #2662",
		"Reproduces: a caller reading the widget got its parts in the order they\n  were appended rather than the order they were declared.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	} {
		if !strings.Contains(committed, want) {
			t.Fatalf("the squash commit must carry the branch's own %q trailer past a trailing issue reference, got:\n%s", want, committed)
		}
	}
	if strings.Contains(committed, "assembled backwards") {
		t.Fatalf("only the branch's trailers belong beneath the review name, not its prose, got:\n%s", committed)
	}
}

// TestGateMergeTrailersFromBodyReadsEveryDeclaredTrailer pins the boundary that
// replaced the block walk, and the two shapes the walk decided wrongly in both
// directions. A declaration sitting above a trailing paragraph the walk did not
// recognise is carried; a line the closed set does not name is not carried
// wherever it sits, so dropping the boundary did not widen what lands on the
// target.
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

// TestGateMergeTrailersFromBodyIsEmptyWhenTheBodyDeclaresNoTrailers pins the
// ordinary case that must not regress: a message that declares nothing carried
// yields nothing, so a body of ordinary prose is read as prose.
func TestGateMergeTrailersFromBodyIsEmptyWhenTheBodyDeclaresNoTrailers(t *testing.T) {
	body := "Fix the widget\n\n" +
		"The widget was assembled backwards, so every caller reading it got\n" +
		"the parts in the wrong order.\n"
	if got := gateMergeTrailersFromBody(body); len(got) != 0 {
		t.Fatalf("expected no carried trailers, got %q", got)
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
