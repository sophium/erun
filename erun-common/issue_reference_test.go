package eruncommon

import "testing"

// TestBranchIssueNumberReadsTheDocumentedConvention pins the four states the
// derivation answers for: both documented branch prefixes name their issue,
// and everything else names nothing. The "nothing" half carries the weight --
// a branch that merely contains a number is not a link to the issue with that
// number, and a derivation that guesses one attaches a review to work nobody
// named it for.
func TestBranchIssueNumberReadsTheDocumentedConvention(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		branch string
		want   string
	}{
		{"a bug branch names its issue", "bug/2212-issue-ref-from-branch", "2212"},
		{"a feature branch names its issue", "feature/2212-issue-ref-from-branch", "2212"},
		{"surrounding whitespace does not hide the convention", "  bug/2212-issue-ref  ", "2212"},
		{"a branch without the description separator names nothing", "bug/2212", ""},
		{"a branch whose number is not a number names nothing", "bug/issue-2212", ""},
		{"a branch with no number names nothing", "feature/", ""},
		{"a branch outside the convention names nothing", "bugfix/2212-issue-ref", ""},
		{"a prefix that merely resembles one names nothing", "features/2212-issue-ref", ""},
		{"the convention is the documented lowercase spelling", "Bug/2212-issue-ref", ""},
		{"an ordinary branch names nothing", "main", ""},
		{"an empty branch names nothing", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := BranchIssueNumber(tc.branch); got != tc.want {
				t.Fatalf("BranchIssueNumber(%q) = %q, want %q", tc.branch, got, tc.want)
			}
		})
	}
}

// TestResolveIssueReferencePrefersADeclaredReferenceOverADerivableOne is the
// state a careless implementation gets wrong. Here the branch names one issue
// and the caller has named a different one, so the two answers disagree. The
// declared reference is the only one the platform actually knows -- the branch
// was either renamed or the work was deliberately reassigned -- and overruling
// it with a string match would silently relink a caller's work to whatever
// their branch name happens to look like.
func TestResolveIssueReferencePrefersADeclaredReferenceOverADerivableOne(t *testing.T) {
	t.Parallel()
	resolved, ok := ResolveIssueReference("owner/repo#2109", "bug/2212-issue-ref-from-branch")

	if !ok {
		t.Fatal("ResolveIssueReference reported no issue, want the declared one")
	}
	if resolved.Ref != "owner/repo#2109" {
		t.Fatalf("Ref = %q, want the declared reference rather than the branch's own number", resolved.Ref)
	}
	if resolved.Source != IssueReferenceDeclared {
		t.Fatalf("Source = %q, want %q: a reference the caller stated is not an inference", resolved.Source, IssueReferenceDeclared)
	}
}

// TestResolveIssueReferenceMarksABranchDerivedIssueAsInferred: with nothing
// declared, the branch is the only source, and the answer has to say so. This
// is the half that keeps an inferred link from being written back as though it
// had been declared.
func TestResolveIssueReferenceMarksABranchDerivedIssueAsInferred(t *testing.T) {
	t.Parallel()
	resolved, ok := ResolveIssueReference("", "feature/2212-issue-ref-from-branch")

	if !ok {
		t.Fatal("ResolveIssueReference reported no issue, want the branch's own number")
	}
	if resolved.Ref != "2212" {
		t.Fatalf("Ref = %q, want the number the branch names", resolved.Ref)
	}
	if resolved.Source != IssueReferenceInferred {
		t.Fatalf("Source = %q, want %q", resolved.Source, IssueReferenceInferred)
	}
}

// TestResolveIssueReferenceReportsNoIssueForABranchOutsideTheConvention: the
// derivation is best-effort, so "no answer" is a real answer and not a reason
// to fall back to a guess. ok being false is what tells a caller to leave the
// work unlinked.
func TestResolveIssueReferenceReportsNoIssueForABranchOutsideTheConvention(t *testing.T) {
	t.Parallel()
	resolved, ok := ResolveIssueReference("  ", "bugfix/2212-issue-ref")

	if ok {
		t.Fatalf("ResolveIssueReference resolved %+v for a branch outside the convention, want no issue at all", resolved)
	}
	if resolved != (IssueReference{}) {
		t.Fatalf("ResolveIssueReference = %+v, want the zero reference alongside ok=false", resolved)
	}
}
