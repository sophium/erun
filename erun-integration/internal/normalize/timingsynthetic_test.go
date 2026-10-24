package normalize

import "testing"

// TestSyntheticTimingRowsAreStripped: whether a synthetic timing row appears at
// all depends on wall clock, so Apply drops the whole line.
func TestSyntheticTimingRowsAreStripped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"unaccounted row at depth 1 is dropped",
			"step timing (ordered by duration):\n" +
				"  deploy [2.1s]\n" +
				"    (unaccounted) [134ms]\n" +
				"    the-release [1.9s]\n",
			"step timing (ordered by duration):\n" +
				"  deploy [<ELAPSED>]\n" +
				"    the-release [<ELAPSED>]\n",
		},
		{
			"overlap row at depth 2 is dropped",
			"  release [10s]\n" +
				"    push [8s]\n" +
				"      (ran concurrently, overlap) [500ms]\n" +
				"      linux/amd64 [4s]\n" +
				"      linux/arm64 [4s]\n",
			"  release [<ELAPSED>]\n" +
				"    push [<ELAPSED>]\n" +
				"      linux/amd64 [<ELAPSED>]\n" +
				"      linux/arm64 [<ELAPSED>]\n",
		},
		{
			"a step genuinely named unaccounted-looking text without brackets is untouched",
			"unaccounted work remains\n",
			"unaccounted work remains\n",
		},
		{
			"no synthetic rows leaves the tree untouched",
			"step timing (ordered by duration):\n  deploy [2.1s]\n",
			"step timing (ordered by duration):\n  deploy [<ELAPSED>]\n",
		},
	}
	for _, c := range cases {
		if got := Apply(c.in); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}
