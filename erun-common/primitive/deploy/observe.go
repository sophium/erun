package deploy

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// RunObserveKubectl returns stderr separately so callers can classify "not found" without parsing a wrapped error.
func RunObserveKubectl(args []string) (stdout []byte, stderr string, err error) {
	cmd := core.Command("kubectl", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

// KubectlErrorMessage folds sanitized stderr into err, collapsing klog's per-retry
// "Unhandled Error" frames.
func KubectlErrorMessage(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	if sanitized := sanitizeKubectlFailureOutput(stderr); sanitized != "" {
		return fmt.Errorf("%w: %s", err, sanitized)
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
