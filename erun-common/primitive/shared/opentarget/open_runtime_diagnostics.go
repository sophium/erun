package opentarget

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
)

type OpenKubectlRunnerFunc func(args []string, stdout, stderr io.Writer) error

func RunOpenKubectl(args []string, stdout, stderr io.Writer) error {
	cmd := core.Command("kubectl", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	return cmd.Run()
}
