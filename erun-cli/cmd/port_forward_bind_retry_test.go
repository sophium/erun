package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// kubectlListenConflict is the failure measured at 58% of
// reattachments, copied from the reported log: the replacement forward's
// kubectl cannot bind because the listener it replaced has not finished
// closing. Both lines are kubectl's; the second is what marks the failure as a
// listen failure rather than a forward that never got that far.
const kubectlListenConflict = `Unable to listen on port 26100: Listeners failed to create with the following errors: [unable to create listener: Error listen tcp4 127.0.0.1:26100: bind: address already in use]
error: unable to listen on any of the requested ports: [{26100 26100}]
`

func appendLogFile(logPath, text string) error {
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = file.WriteString(text)
	return err
}

func appendToLog(t *testing.T, logPath, text string) {
	t.Helper()
	if err := appendLogFile(logPath, text); err != nil {
		t.Fatalf("append to %s: %v", logPath, err)
	}
}

func testForwardContext() (common.Context, *bytes.Buffer) {
	var out bytes.Buffer
	return common.Context{Logger: common.NewLoggerWithWriters(0, &out, &out)}, &out
}

// TestStartPortForwardWithBindRetryAbsorbsTheDyingListenersSocket is the
// regression test: a replacement forward whose first listen fails
// because the predecessor's socket is still closing must be started again
// rather than reported as a failed reattachment.
func TestStartPortForwardWithBindRetryAbsorbsTheDyingListenersSocket(t *testing.T) {
	ctx, out := testForwardContext()
	logPath := filepath.Join(t.TempDir(), "mcp.log")
	attempts := 0
	process, err := startPortForwardWithBindRetry(ctx, "mcp", 26100, func() (*os.Process, error) {
		attempts++
		if attempts < 3 {
			appendToLog(t, logPath, kubectlListenConflict)
			return nil, fmt.Errorf("%w: see %s", errPortForwardListenConflict, logPath)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("a listen that fails only while the old socket is closing must be retried, got %v", err)
	}
	if process != nil {
		t.Fatalf("expected the successful attempt's process (nil here), got %v", process)
	}
	if attempts != 3 {
		t.Fatalf("expected the start to be retried until it succeeded (3 attempts), got %d", attempts)
	}
	if !strings.Contains(out.String(), "still held by the previous listener's closing socket") {
		t.Fatalf("expected the retry to be traced for the operator, got:\n%s", out.String())
	}
}

// TestStartPortForwardWithBindRetryIsBounded pins the other half of the
// contract: a port that does not come free is not retried forever. A port
// genuinely held by something else ends as a reported failure, after a window
// short enough to be a retry rather than a hang.
func TestStartPortForwardWithBindRetryIsBounded(t *testing.T) {
	ctx, _ := testForwardContext()
	attempts := 0
	started := time.Now()
	_, err := startPortForwardWithBindRetry(ctx, "sshd", 26100, func() (*os.Process, error) {
		attempts++
		return nil, fmt.Errorf("%w: held by PID 4242", errPortForwardListenConflict)
	})
	if !errors.Is(err, errPortForwardListenConflict) {
		t.Fatalf("a port that never comes free must still fail, got %v", err)
	}
	if attempts != portForwardBindRetryAttempts {
		t.Fatalf("expected exactly %d attempts before giving up, got %d", portForwardBindRetryAttempts, attempts)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the retry window must stay short, took %s", elapsed)
	}
}

// TestStartPortForwardWithBindRetryDoesNotAbsorbOtherFailures is what keeps the
// retry from becoming a blanket "try again": only the closing-socket failure is
// absorbed. A forward that failed for any other reason — here, a pod that is
// not running — is returned on the first attempt, unretried and unreworded.
func TestStartPortForwardWithBindRetryDoesNotAbsorbOtherFailures(t *testing.T) {
	ctx, _ := testForwardContext()
	attempts := 0
	podGone := errors.New("error: unable to forward port because pod is not running. Current status=Pending")
	_, err := startPortForwardWithBindRetry(ctx, "mcp", 26100, func() (*os.Process, error) {
		attempts++
		return nil, podGone
	})
	if !errors.Is(err, podGone) {
		t.Fatalf("expected the original failure back, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("only a listen conflict may be retried; got %d attempts", attempts)
	}
}

// TestPortForwardLogReportsListenConflict pins the detection itself: kubectl's
// two listen lines, and nothing else in the log, mark the failure as the one
// worth retrying.
func TestPortForwardLogReportsListenConflict(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "forward.log")
	appendToLog(t, logPath, "Forwarding from 127.0.0.1:26100 -> 26100\n")
	appendToLog(t, logPath, "error: lost connection to pod\n")
	if portForwardLogReportsListenConflict(logPath, 0) {
		t.Fatal("a dropped connection is not a listen conflict")
	}

	// Only what this attempt appended counts: the conflict lines above are
	// stale the moment they are read again, and a later failure that is not a
	// conflict must not inherit them.
	staleEnd := portForwardLogSize(logPath)
	appendToLog(t, logPath, kubectlListenConflict)
	if !portForwardLogReportsListenConflict(logPath, staleEnd) {
		t.Fatal("kubectl's address-in-use listen failure must be recognised")
	}
	if portForwardLogReportsListenConflict(logPath, portForwardLogSize(logPath)) {
		t.Fatal("a previous attempt's conflict lines must not be re-read as this attempt's")
	}
}

// TestWaitForMCPPortForwardStopsOnListenConflict is the wait's half of the
// fix. Before it, a failed listen was indistinguishable from a pod that never
// answered: the wait spent its whole five-second startup timeout and reported a
// timeout, which is both the wrong diagnosis and far too slow for the retry
// above to run within.
func TestWaitForMCPPortForwardStopsOnListenConflict(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "mcp.log")
	// kubectl writes its failed listen from the child process while the wait
	// polls, so the conflict has to arrive mid-wait to be this attempt's — the
	// same ordering production sees, and the ordering that makes the offset the
	// wait captured at entry the right thing to read from.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = appendLogFile(logPath, kubectlListenConflict)
	}()

	// Port 1 on loopback: nothing listens there, so the probe fails
	// immediately and the log is the only thing left to decide on.
	started := time.Now()
	err := waitForMCPPortForward(1, logPath)
	if !errors.Is(err, errPortForwardListenConflict) {
		t.Fatalf("expected the listen conflict to be reported as such, got %v", err)
	}
	if !strings.Contains(err.Error(), logPath) {
		t.Fatalf("the failure must name the log holding kubectl's own words, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > mcpPortForwardStartupTimeout/2 {
		t.Fatalf("the wait must end on the conflict rather than run out its timeout, took %s", elapsed)
	}
}
