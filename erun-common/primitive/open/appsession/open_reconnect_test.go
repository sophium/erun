package appsession

import (
	"strings"
	"testing"
)

func TestParseRemoteAppSessionIDs(t *testing.T) {
	lsOutput := strings.Join([]string{
		"erun-local-open-0.dtach",
		"erun-local-open-1.dtach",
		"erun-local-ai.dtach",
		"erun-local-ai.owner",
		"erun-local-contribute-ai.dtach",
		"other-env-open-2.dtach",
		"",
		"not-a-socket",
	}, "\n")
	got := ParseRemoteAppSessionIDs("erun", "local", lsOutput)
	want := []string{"open-0", "open-1", "ai", "contribute-ai"}
	if len(got) != len(want) {
		t.Fatalf("ParseRemoteAppSessionIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseRemoteAppSessionIDs = %v, want %v", got, want)
		}
	}
}

// An unidentifiable master kills nothing but still drops the socket.
func TestRemoteAppSessionEndScript(t *testing.T) {
	script := RemoteAppSessionEndScript("erun", "local", "open-2")
	for _, want := range []string{
		`[ "$child_comm" != "dtach" ]`,
		`if [ -n "$master_pid" ]; then kill "$master_pid" 2>/dev/null || true; fi`,
		`rm -f "/tmp/erun-sessions/erun-local-open-2.dtach" "/tmp/erun-sessions/erun-local-open-2.owner"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("end script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "dtach -A") {
		t.Fatalf("end script must not attach:\n%s", script)
	}
}
