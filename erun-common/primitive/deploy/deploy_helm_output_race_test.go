package deploy

import (
	"os/exec"
	"strings"
	"testing"
)

// TestConfigureHelmDeployCmdOutputConcurrentStreams runs a real child process
// that writes on both stdout and stderr at once through the exact wiring
// configureHelmDeployCmdOutput builds. os/exec runs one copier goroutine per
// stream, and helmOutput is reachable from both (as cmd.Stdout and through
// cmd.Stderr's io.MultiWriter), so it must be synchronized. Run with -race: it
// must report no race, and both streams must still be captured.
func TestConfigureHelmDeployCmdOutputConcurrentStreams(t *testing.T) {
	cmd := exec.Command("sh", "-c", `for i in $(seq 1 400); do echo "out-$i"; echo "err-$i" >&2; done`)
	params := HelmDeployParams{}

	helmOutput, stderr := configureHelmDeployCmdOutput(cmd, params)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	combined := helmOutput.combined()
	if !strings.Contains(combined, "out-1") {
		t.Errorf("combined helm output missing stdout content: %q", truncate(combined))
	}
	if !strings.Contains(combined, "err-1") {
		t.Errorf("combined helm output missing stderr content: %q", truncate(combined))
	}

	stderrText := stderr.String()
	if !strings.Contains(stderrText, "err-1") {
		t.Errorf("separately-returned stderr missing its own content: %q", truncate(stderrText))
	}
	if strings.Contains(stderrText, "out-1") {
		t.Errorf("separately-returned stderr must not carry stdout content, got: %q", truncate(stderrText))
	}
}

func truncate(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "…"
}
