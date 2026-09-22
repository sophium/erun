package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// A replacement forward is started moments after the forward it replaces was
// stopped, and the predecessor's listening socket outlives the process that
// held it: the kill returns, the port stops accepting, and the kernel still
// refuses a bind on it for a short while. kubectl reports that refusal as
// "Unable to listen on port N ... address already in use" and exits.
//
// So a listen that fails because the port is still held is not a failed
// attempt. It is the same attempt started a moment too early, and the answer is
// to wait the socket out rather than to treat the port as taken. Only that
// failure is absorbed: a pod that is not running, a cluster that is not
// answering, a state file that cannot be written — every other error is
// returned on the first try, unretried. A port genuinely held by something that
// is not going away exhausts these attempts and is reported just as loudly,
// naming the log that holds kubectl's own words.
const (
	// portForwardBindRetryAttempts bounds one start. The socket being waited out
	// is a close in progress, not a lease, so the whole window stays short: ten
	// attempts a tenth of a second apart is one second — the same budget
	// waitForLocalPortToClose already spends, and nowhere near long enough to
	// disguise a port that is genuinely taken.
	portForwardBindRetryAttempts = 10
	// portForwardBindRetryInterval is how long each retry waits before starting
	// kubectl again. Fixed rather than backing off: the thing being waited for
	// is a socket teardown, which completes in tens of milliseconds when it
	// completes at all.
	portForwardBindRetryInterval = 100 * time.Millisecond
)

// errPortForwardListenConflict marks a startup attempt that ended because the
// local port could not be bound, rather than because nothing answered through
// it. Only this error is retried, so the two have to be distinguishable: a wait
// that runs out against a pod that is not running looks identical from the
// outside and must not be mistaken for a dying socket.
var errPortForwardListenConflict = errors.New("the local port could not be bound")

// startPortForwardWithBindRetry runs attempt until it succeeds, retrying it
// while — and only while — it fails because the predecessor's socket has not
// finished closing. attempt must be safe to call again after a prior call
// failed: each call launches its own kubectl and rewrites its own state.
//
// The last process an attempt started is returned alongside the error that
// ended the loop, so a caller's own failure path still has something to
// release.
func startPortForwardWithBindRetry(ctx common.Context, kind string, localPort int, attempt func() (*os.Process, error)) (*os.Process, error) {
	for try := 1; ; try++ {
		process, err := attempt()
		if err == nil {
			return process, nil
		}
		if try >= portForwardBindRetryAttempts || !errors.Is(err, errPortForwardListenConflict) {
			return process, err
		}
		if process != nil {
			// The attempt that got far enough to launch kubectl also saw it
			// exit on the failed listen; this only reaps the corpse.
			_ = process.Kill()
		}
		ctx.Trace(fmt.Sprintf("%s: 127.0.0.1:%d is still held by the previous listener's closing socket; retrying the port-forward (attempt %d of %d)",
			kind, localPort, try+1, portForwardBindRetryAttempts))
		time.Sleep(portForwardBindRetryInterval)
	}
}

// listenConflictError names the failure the bind retry exists for, in place of
// the "timed out waiting" wording a failed listen used to collect: kubectl
// exited on a bind refusal, which is a different thing from a forward that
// never answered, and reporting the timeout would send the operator looking for
// a pod problem they do not have. It still names the log, which holds kubectl's
// own words about which port and why.
func listenConflictError(localPort int, logPath string) error {
	return fmt.Errorf("%w: kubectl could not listen on 127.0.0.1:%d; the port is still held — see %s", errPortForwardListenConflict, localPort, logPath)
}

// launchPortForwardProcessRetrying runs launch through the transient
// OS-allocation retry (see retryTransientPortForwardStart): a momentary
// fd/process-table squeeze on a loaded host is worth a few fast retries. A
// listen conflict is the bind retry's to absorb instead, and comes back from
// here unreconciled because it is not an allocation failure.
func launchPortForwardProcessRetrying(launch func() (*os.Process, error)) (*os.Process, error) {
	var process *os.Process
	if err := retryTransientPortForwardStart(func() error {
		launched, err := launch()
		if err != nil {
			return err
		}
		process = launched
		return nil
	}); err != nil {
		return nil, err
	}
	return process, nil
}

// portForwardLogSize is where a start's own log output begins, so the conflict
// check below reads only what this attempt wrote. The log is append-only across
// every forward an environment has ever had, and a stale "Unable to listen"
// line from an earlier attempt must not sentence a later, unrelated failure to
// a retry.
func portForwardLogSize(logPath string) int64 {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

// portForwardLogReportsListenConflict reports whether the bytes appended to
// logPath since since are kubectl's address-in-use listen failure. The two
// markers are kubectl's own phrasing, not the OS's: the platform-specific
// wording underneath them ("bind: address already in use" on unix, a WSA
// variant on Windows) is what varies, and this has to hold on every host.
//
// since is taken before the attempt launches, so everything the attempt's
// kubectl wrote is after it. An attempt that fails fast — a stub, a host where
// kubectl exits in microseconds — can otherwise have written its whole failure
// before the wait takes its first look, and a later offset would step over it.
func portForwardLogReportsListenConflict(logPath string, since int64) bool {
	file, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()
	// A log that was rotated between the offset being taken and this read is
	// now shorter than the offset. Everything in it then belongs to this
	// attempt, so read it whole rather than seeking past its end and seeing
	// nothing; a rotation only ever truncates, so nothing older can reappear.
	if since > 0 {
		if info, statErr := file.Stat(); statErr == nil && since <= info.Size() {
			if _, err := file.Seek(since, io.SeekStart); err != nil {
				return false
			}
		}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return false
	}
	return isListenConflictText(string(data))
}

// isListenConflictText is the text half of the check above, shared with the
// timeout diagnosis so an operator who reaches that path is told the same thing
// this retry acted on.
func isListenConflictText(log string) bool {
	value := strings.ToLower(log)
	return strings.Contains(value, "unable to listen on port") &&
		strings.Contains(value, "unable to listen on any of the requested ports")
}
