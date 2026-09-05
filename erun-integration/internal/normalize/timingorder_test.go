package normalize

import "testing"

// TestStepTimingSiblingsCanonicalizeToNameOrder is the regression for the
// step-timing table's remaining wall-clock-derived signal: with durations
// already redacted to "[<ELAPSED>]", the only thing left that can differ
// between two runs of the same fixture is which regime (name-tiebreak vs.
// duration order) a given set of near-instant siblings measured into. Apply
// must produce the same sibling order regardless of which regime the
// production process actually hit, by canonicalizing every level to name
// order. Drives Apply directly so it runs on every OS.
func TestStepTimingSiblingsCanonicalizeToNameOrder(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"two siblings recorded in duration order canonicalize to name order",
			"step timing (ordered by duration):\n" +
				"  release [1.9s]\n" +
				"    push [1.1s]\n" +
				"    post-release-version-bump [40ms]\n",
			"step timing (ordered by duration):\n" +
				"  release [<ELAPSED>]\n" +
				"    post-release-version-bump [<ELAPSED>]\n" +
				"    push [<ELAPSED>]\n",
		},
		{
			"the same siblings recorded in the opposite order canonicalize identically",
			"step timing (ordered by duration):\n" +
				"  release [1.9s]\n" +
				"    post-release-version-bump [1.1s]\n" +
				"    push [40ms]\n",
			"step timing (ordered by duration):\n" +
				"  release [<ELAPSED>]\n" +
				"    post-release-version-bump [<ELAPSED>]\n" +
				"    push [<ELAPSED>]\n",
		},
		{
			"nested subtrees move with their root and sort at every level",
			"  release [10s]\n" +
				"    push [1s]\n" +
				"    publish [9s]\n" +
				"      base [5s]\n" +
				"        linux/arm64 [3s]\n" +
				"        linux/amd64 [2s]\n" +
				"      api [4s]\n",
			"  release [<ELAPSED>]\n" +
				"    publish [<ELAPSED>]\n" +
				"      api [<ELAPSED>]\n" +
				"      base [<ELAPSED>]\n" +
				"        linux/amd64 [<ELAPSED>]\n" +
				"        linux/arm64 [<ELAPSED>]\n" +
				"    push [<ELAPSED>]\n",
		},
		{
			"a single root with one child is untouched beyond redaction",
			"step timing (ordered by duration):\n  deploy [2.1s]\n    the-release [1.9s]\n",
			"step timing (ordered by duration):\n  deploy [<ELAPSED>]\n    the-release [<ELAPSED>]\n",
		},
		{
			"non-timing text is left alone",
			"the release is not a timing row\n",
			"the release is not a timing row\n",
		},
	}
	for _, c := range cases {
		if got := Apply(c.in); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}
