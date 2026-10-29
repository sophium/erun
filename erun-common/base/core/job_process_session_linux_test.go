//go:build linux

package core

import (
	"testing"
)

func TestParseProcStatReadsStateAndSession(t *testing.T) {
	live := "4243 (a b) c) S 1 4243 4242 0 -1 4194304 91 0 0 0 0 0 0 0 20 0 1 0 0 0 0\n"
	proc, ok := parseProcStat([]byte(live))
	if !ok {
		t.Fatalf("a well-formed /proc/<pid>/stat must parse")
	}
	if proc.Pid != 4243 || proc.session != 4242 || proc.Zombie {
		t.Fatalf("parsed %+v, want pid 4243 in session 4242 and alive", proc)
	}

	zombie := "4243 (sleep) Z 1 4243 4242 0 -1 4194304 91 0 0 0 0 0 0 0 20 0 1 0 0 0 0\n"
	proc, ok = parseProcStat([]byte(zombie))
	if !ok || !proc.Zombie {
		t.Fatalf("parsed %+v (ok=%v), want a zombie", proc, ok)
	}

	for _, malformed := range []string{"", "4243", "4243 (sleep) S 1 4243\n", "notapid (sleep) S 1 4243 4242\n"} {
		if _, ok := parseProcStat([]byte(malformed)); ok {
			t.Fatalf("malformed stat %q must not parse", malformed)
		}
	}
}
