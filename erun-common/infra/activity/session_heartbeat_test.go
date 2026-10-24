package activity

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// No dtach master is alive here, so every socket reports PID 0. The live direction is
// covered in erun-ui (TestSessionRunningFollowsHeartbeatNotStreamSilence).
func TestRemoteAppSessionHeartbeatScriptReportsSocketsWithNoLiveMaster(t *testing.T) {
	// Not t.TempDir(): unix socket paths are capped near 104 bytes.
	dir, err := os.MkdirTemp("", "erun-sock")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for _, name := range []string{"team-dev-open-0.dtach", "team-dev-ai.dtach", "other-env-open-0.dtach"} {
		listener, err := net.Listen("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create socket %s: %v", name, err)
		}
		t.Cleanup(func() { _ = listener.Close() })
	}
	if err := os.WriteFile(filepath.Join(dir, "team-dev-stray.dtach"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	script := remoteAppSessionHeartbeatScriptIn(dir, "team", "dev")
	output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("run heartbeat script: %v\n%s", err, output)
	}

	heartbeats := ParseRemoteAppSessionHeartbeats(string(output))
	ids := make([]string, 0, len(heartbeats))
	for _, heartbeat := range heartbeats {
		if heartbeat.Running() {
			t.Fatalf("socket with no dtach master must not report running: %+v", heartbeat)
		}
		ids = append(ids, heartbeat.ID)
	}
	if got := strings.Join(ids, ","); got != "ai,open-0" {
		t.Fatalf("expected this env's sockets only, got %q", got)
	}
	if running := RunningRemoteAppSessions(heartbeats); running != 0 {
		t.Fatalf("expected no running sessions, got %d", running)
	}
}

// An unmatched glob must not run the loop once with a literal `*.dtach`.
func TestRemoteAppSessionHeartbeatScriptIsSilentWithoutSockets(t *testing.T) {
	script := remoteAppSessionHeartbeatScriptIn(t.TempDir(), "team", "dev")
	output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("run heartbeat script: %v\n%s", err, output)
	}
	if heartbeats := ParseRemoteAppSessionHeartbeats(string(output)); len(heartbeats) != 0 {
		t.Fatalf("expected no heartbeats, got %+v", heartbeats)
	}
}

func TestParseRemoteAppSessionHeartbeats(t *testing.T) {
	output := strings.Join([]string{
		"some unrelated pod chatter",
		"erun-session\tai\t4211\tclaude",
		"erun-session\topen-0\t0\t",
		"erun-session\t\t99\tbash",
		"erun-session\tmalformed",
	}, "\n")

	heartbeats := ParseRemoteAppSessionHeartbeats(output)
	if len(heartbeats) != 3 {
		t.Fatalf("expected 3 parsed heartbeats, got %+v", heartbeats)
	}
	if !heartbeats[0].Running() || heartbeats[0].PID != 4211 || heartbeats[0].Program != "claude" {
		t.Fatalf("unexpected AI heartbeat: %+v", heartbeats[0])
	}
	if heartbeats[1].Running() {
		t.Fatalf("pid 0 must not report running: %+v", heartbeats[1])
	}
	if heartbeats[2].ID != "malformed" || heartbeats[2].Running() {
		t.Fatalf("a line without fields must parse as a not-running session: %+v", heartbeats[2])
	}
	if running := RunningRemoteAppSessions(heartbeats); running != 1 {
		t.Fatalf("expected exactly one running session, got %d", running)
	}
}
