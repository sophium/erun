//go:build linux

package eruncommon

import "testing"

// /proc/<pid>/stat is the Linux half of the session scan, and the command name
// it carries is the one field that may contain spaces and parentheses of its
// own — a process named `a b) c` must not shift the state, process group and
// session fields the scan reads.
func TestParseProcStatReadsStateAndSession(t *testing.T) {
	live := "4243 (a b) c) S 1 4243 4242 0 -1 4194304 91 0 0 0 0 0 0 0 20 0 1 0 0 0 0\n"
	proc, ok := parseProcStat([]byte(live))
	if !ok {
		t.Fatalf("a well-formed /proc/<pid>/stat must parse")
	}
	if proc.pid != 4243 || proc.session != 4242 || proc.zombie {
		t.Fatalf("parsed %+v, want pid 4243 in session 4242 and alive", proc)
	}

	// A zombie is the same row with state Z; this is the one the ps-based
	// check also had to read as completed rather than abandoned work.
	zombie := "4243 (sleep) Z 1 4243 4242 0 -1 4194304 91 0 0 0 0 0 0 0 20 0 1 0 0 0 0\n"
	proc, ok = parseProcStat([]byte(zombie))
	if !ok || !proc.zombie {
		t.Fatalf("parsed %+v (ok=%v), want a zombie", proc, ok)
	}

	for _, malformed := range []string{"", "4243", "4243 (sleep) S 1 4243\n", "notapid (sleep) S 1 4243 4242\n"} {
		if _, ok := parseProcStat([]byte(malformed)); ok {
			t.Fatalf("malformed stat %q must not parse", malformed)
		}
	}
}
