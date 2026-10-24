package deploy

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// RunObserveKubectl runs a read-only kubectl get, returning stderr alongside
// any error so callers can distinguish "not found" and "unknown resource
// type" from a real failure without re-parsing a wrapped error string.
func RunObserveKubectl(args []string) (stdout []byte, stderr string, err error) {
	cmd := core.Command("kubectl", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

// KubectlErrorMessage folds stderr into err, sanitizing kubectl's raw output
// through sanitizeKubectlFailureOutput first: an unreachable API server makes
// kubectl retry and emit klog's "Unhandled Error" frame once per retry, and
// without this every goroutine id and source location would reach the
// operator verbatim, repeated.
func KubectlErrorMessage(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	if sanitized := sanitizeKubectlFailureOutput(stderr); sanitized != "" {
		return fmt.Errorf("%w: %s", err, sanitized)
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
