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
			// A failed step renders its error after the duration, so the row
			// no longer ends at the bracket. That shape used to fall out of
			// the canonicalizer entirely, which ended the block at it and left
			// every sibling after it in the order the run measured — the whole
			// point of this normalization, silently off in exactly the
			// scenarios that induce a failure on purpose.
			"a failed row's error suffix does not stop the canonicalization",
			"  push [9s]\n" +
				"    base (cache miss: x) [4s]\n" +
				"      linux/arm64 (cache miss: x) [2s]\n" +
				"      linux/amd64 (cache miss: x) [1s]\n" +
				"    api (cache miss: x) [5s]\n" +
				"      linux/arm64 (cache miss: x) [3s]\n" +
				"      linux/amd64 (failed) (cache miss: x) [2s] — unauthorized: denied\n" +
				"      linux/amd64 (cache miss: x) [1s]\n" +
				"    chart base [1s]\n" +
				"    chart api [1s]\n",
			"  push [<ELAPSED>]\n" +
				"    api (cache miss: x) [<ELAPSED>]\n" +
				"      linux/amd64 (cache miss: x) [<ELAPSED>]\n" +
				"      linux/amd64 (failed) (cache miss: x) [<ELAPSED>] — unauthorized: denied\n" +
				"      linux/arm64 (cache miss: x) [<ELAPSED>]\n" +
				"    base (cache miss: x) [<ELAPSED>]\n" +
				"      linux/amd64 (cache miss: x) [<ELAPSED>]\n" +
				"      linux/arm64 (cache miss: x) [<ELAPSED>]\n" +
				"    chart api [<ELAPSED>]\n" +
				"    chart base [<ELAPSED>]\n",
		},
		{
			// An error can run to several lines (the missing-binfmt refusal
			// ends with a command on its own line). Those lines are part of
			// the row above them and have to travel with it when siblings are
			// reordered, not be read as rows of their own.
			"a multi-line error stays attached to the row it belongs to",
			"  build (failed) [9s] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"    base (failed) [5s] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"    api (failed) [4s] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"timing record written to somewhere\n",
			"  build (failed) [<ELAPSED>] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"    api (failed) [<ELAPSED>] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"    base (failed) [<ELAPSED>] — cannot build linux/arm64. retry:\n" +
				"  docker run --privileged --rm tonistiigi/binfmt --install all\n" +
				"timing record written to somewhere\n",
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
