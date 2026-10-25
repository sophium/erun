package recovery

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

func ClearHelmReleasePendingOperation(params helmdeploy.HelmReleaseRecoveryParams) error {
	if strings.TrimSpace(params.ReleaseName) == "" {
		return fmt.Errorf("helm release name is required")
	}
	if strings.TrimSpace(params.Namespace) == "" {
		return fmt.Errorf("helm release namespace is required")
	}

	command := HelmReleaseRecoveryCommand(params)
	cmd := core.Command(command.Name, command.Args...)
	capture := new(bytes.Buffer)
	if params.Verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(params.Stdout, capture)
		cmd.Stderr = core.TeeWriter(params.Stderr, capture)
	} else {
		cmd.Stdout = capture
		cmd.Stderr = capture
	}
	if err := cmd.Run(); err != nil {
		if params.Verbosity < core.VerbosityDebug {
			if output := strings.TrimSpace(capture.String()); output != "" {
				return fmt.Errorf("%w\n%s", err, output)
			}
		}
		return err
	}
	return nil
}

func IsHelmReleasePendingOperationMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "another operation") &&
		strings.Contains(message, "install/upgrade/rollback") &&
		strings.Contains(message, "in progress")
}
