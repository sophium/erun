package portfwd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// A predecessor's socket briefly blocks rebinding after its process dies. Only that
// listen conflict is retried; every other start error returns immediately.
// One second in total: enough for a socket teardown, too short to mask a taken port.
const (
	portForwardBindRetryAttempts = 10
	portForwardBindRetryInterval = 100 * time.Millisecond
)

// errPortForwardListenConflict marks a failed bind, distinct from a forward that never answered.
var errPortForwardListenConflict = errors.New("the local port could not be bound")

// StartPortForwardWithBindRetry retries attempt only on listen conflicts. attempt must be
// safe to repeat; the last started process is returned so the caller can release it.
func StartPortForwardWithBindRetry(ctx core.Context, kind string, localPort int, attempt func() (*os.Process, error)) (*os.Process, error) {
	for try := 1; ; try++ {
		process, err := attempt()
		if err == nil {
			return process, nil
		}
		if try >= portForwardBindRetryAttempts || !errors.Is(err, errPortForwardListenConflict) {
			return process, err
		}
		if process != nil {
			// kubectl already exited on the failed listen; this only reaps it.
			_ = process.Kill()
		}
		ctx.Trace(fmt.Sprintf("%s: 127.0.0.1:%d is still held by the previous listener's closing socket; retrying the port-forward (attempt %d of %d)",
			kind, localPort, try+1, portForwardBindRetryAttempts))
		time.Sleep(portForwardBindRetryInterval)
	}
}

// ListenConflictError reports a bind refusal rather than a startup timeout.
func ListenConflictError(localPort int, logPath string) error {
	return fmt.Errorf("%w: kubectl could not listen on 127.0.0.1:%d; the port is still held — see %s", errPortForwardListenConflict, localPort, logPath)
}

// LaunchPortForwardProcessRetrying retries launch on transient OS allocation failures only.
func LaunchPortForwardProcessRetrying(launch func() (*os.Process, error)) (*os.Process, error) {
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

// PortForwardLogSize marks where an attempt's output starts, so stale conflict lines are ignored.
func PortForwardLogSize(logPath string) int64 {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

// PortForwardLogReportsListenConflict reports whether logPath gained kubectl's address-in-use
// failure after since. since must be taken before launch, or a fast failure is missed.
func PortForwardLogReportsListenConflict(logPath string, since int64) bool {
	file, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()
	// A rotated log is shorter than since and wholly this attempt's, so read it all.
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

func isListenConflictText(log string) bool {
	value := strings.ToLower(log)
	return strings.Contains(value, "unable to listen on port") &&
		strings.Contains(value, "unable to listen on any of the requested ports")
}
