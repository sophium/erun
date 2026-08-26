package normalize

import "testing"

// TestSyntheticTimingRowsAreStripped locks the invariant that a step-timing
// "(unaccounted)"/"(ran concurrently, overlap)" row's presence, not just its
// duration, is decided by wall clock — a stubbed subprocess in a real-run
// scenario costs a different share of the 100ms noise floor on every host, so
// the row can appear on one host's run and not another's. Apply must drop the
// whole line regardless of the row's depth or duration, so its host-dependent
// presence can never fail a golden comparison. Drives Apply directly so it
// runs on every OS.
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
