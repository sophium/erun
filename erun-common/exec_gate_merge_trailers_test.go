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

// TestGateMergeOneSourceCarriesAHardWrappedReproducesTrailer is the
// reproduction of the reported failure: a branch whose "Reproduces:" value is
// hard-wrapped across four physical lines — each continuation starting at
// column 0, which is how a long hand-written value is wrapped and how the
// value on the landing this was observed on was written — landed through the merge
// queue with only its first physical line. The carried entry was truncated
// mid-clause ("...by the boot's own"), and because the truncated line still
// matched the trailer pattern nothing downstream noticed: the landed commit
// asserted a weaker reproduction than its author wrote, and the loss was
// invisible without diffing the branch tip's body against the landed one.
func TestGateMergeOneSourceCarriesAHardWrappedReproducesTrailer(t *testing.T) {
	const branchBody = "Re-hover the orchestrator card a boot landing drops mid-read\n\n" +
		"Reproduces: a hover card closed under a stationary pointer by the boot's own\n" +
		"default-landing open, read again as if it were still there -- expect's 10s\n" +
		"default expires with \"element(s) not found\" for the card that answered the\n" +
		"read before it.\n" +
		"Regression-Test: erun-ui/playwright/tests/areas/orchestrator/orchestrator-restart-required.spec.ts::a hover card dropped while the boot lands is re-hovered, not read as absent\n"
	var committed string
	deps := gateMergeTrailerSeam(branchBody, &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "bug/2459", Message: "Re-hover the orchestrator card a boot landing drops mid-read"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}

	for _, want := range []string{
		"Reproduces: a hover card closed under a stationary pointer by the boot's own\ndefault-landing open, read again as if it were still there -- expect's 10s\ndefault expires with \"element(s) not found\" for the card that answered the\nread before it.",
		"Regression-Test: erun-ui/playwright/tests/areas/orchestrator/orchestrator-restart-required.spec.ts::a hover card dropped while the boot lands is re-hovered, not read as absent",
	} {
		if !strings.Contains(committed, want) {
			t.Fatalf("the squash commit must carry the branch's own %q trailer whole, got:\n%s", want, committed)
		}
	}
}

// TestGateMergeTrailersFromBodyStopsAHardWrappedValueAtItsParagraph is the
// other half of the wrapped-value contract, and the half a naive "keep
// consuming lines until the next trailer" fix fails: a wrapped value must take
// its own paragraph and no more. Two continuations are folded — an indented one
// and a hard-wrapped one — and then the entry stops dead at the blank line
// under it, at the "Closes #N" that follows, and at the unrecognised "Note:"
// under that. A carriage that swallowed the rest of the message would be a
// worse defect than the truncation it replaced, because it would put an
// author's prose on an unattended commit on the target.
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

// TestGateMergeTrailersFromBodyStopsAClosingReferenceAtItsOwnLine is the
// reproduction of the reported over-consumption: a body whose "Closes #N" is
// followed on the very next line — no blank between them — by a sentence of
// prose carried that sentence as part of the declaration, because every
// non-blank line beneath a trailer was read as a continuation of it. The prose
// then reached the unattended commit on the target inside the reported trailer
// set as though the author had declared it.
//
// It is fixable where a continuation opening a "Token:" line is not, because a
// "Closes #N" reference list has no second line to write: the pattern is
// anchored at both ends and every alternative is on the one line, so the
// declaration is complete where it stands. A line beneath it is therefore not a
// reading of the value that is merely unlikely — there is nothing for it to be.
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

// TestGateMergeTrailersFromBodyStopsAContinuableValueAtAnUnrecognisedTrailer
// pins the terminator on a value that is itself continuable, which is the case
// the closing spelling stops exercising once it stands complete on its own line.
// A "Token:" line this squash does not carry still ends the entry above it, so
// the line stays out of the value rather than being folded into it. Treating
// one as a continuation instead is what would carry a real "Defect-Fix: yes" or
// "Refs #N" onto the target as part of the declaration above it.
func TestGateMergeTrailersFromBodyStopsAContinuableValueAtAnUnrecognisedTrailer(t *testing.T) {
	body := "Fix the widget\n\n" +
		"Reproduces: the parts arrived in append order rather than the order they\n" +
		"were declared.\n" +
		"Defect-Fix: yes\n" +
		"Refs #2642\n"

	got := gateMergeTrailersFromBody(body)
	want := []string{"Reproduces: the parts arrived in append order rather than the order they\nwere declared."}
	if len(got) != len(want) {
		t.Fatalf("expected %d carried trailers, got %d: %q", len(want), len(got), got)
	}
	if got[0] != want[0] {
		t.Fatalf("expected the value to end at the unrecognised trailer, got %q", got[0])
	}
}

// TestGateMergeTrailersFromBodyReadsAColumnZeroTokenLineAsANewEntry records the
// residual the wrap fix leaves open. It states what the carrier can decide
// rather than holding the outcome up as wanted: a continuation that starts at
// column 0 with a "Token:" spelling is the same bytes as a trailer entry
// starting there, and a hand-wrapped value's continuation lines are exactly a
// run of such lines, so no rule tells the two apart. Reading the line as an
// entry truncates the value above it; reading it as a continuation is the only
// way to carry a hard-wrapped value whole, and widening the entry to take it
// would fold every genuine line of that shape — the "Defect-Fix: yes" and
// "Refs #N" the terminator above exists for — into the declaration instead.
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
