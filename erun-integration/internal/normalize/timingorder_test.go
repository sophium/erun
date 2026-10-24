package normalize

import "testing"

var stepTimingSiblingCases = []struct {
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
		// Continuation lines travel with their row.
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
		"same-depth rows with no common parent sort by name too",
		"  publish [1.0s]\n" +
			"    api (cache hit) [0.9s]\n" +
			"  post-release-version-bump [1.1s]\n" +
			"  push (failed) [0.8s] — exit status 1\n",
		"  post-release-version-bump [<ELAPSED>]\n" +
			"  publish [<ELAPSED>]\n" +
			"    api (cache hit) [<ELAPSED>]\n" +
			"  push (failed) [<ELAPSED>] — exit status 1\n",
	},
	{
		"a failed row's status suffix does not stop it being a timing row",
		"  push [1.0s]\n" +
			"    publish [0.9s]\n" +
			"    api (failed) [0.8s] — denied: token does not match expected scopes\n",
		"  push [<ELAPSED>]\n" +
			"    api (failed) [<ELAPSED>] — denied: token does not match expected scopes\n" +
			"    publish [<ELAPSED>]\n",
	},
	{
		"a run broken by an interleaved message sorts each fragment",
		"  release (failed) [1.0s] — exit status 1\n" +
			"recover by hand\n" +
			"  sync-develop [0.4s]\n" +
			"  release [0.5s]\n",
		"  release (failed) [<ELAPSED>] — exit status 1\n" +
			"recover by hand\n" +
			"  release [<ELAPSED>]\n" +
			"  sync-develop [<ELAPSED>]\n",
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

// TestStepTimingSiblingsCanonicalizeToNameOrder: Apply sorts timing siblings by
// name, whatever order the run measured.
func TestStepTimingSiblingsCanonicalizeToNameOrder(t *testing.T) {
	for _, c := range stepTimingSiblingCases {
		if got := Apply(c.in); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}
