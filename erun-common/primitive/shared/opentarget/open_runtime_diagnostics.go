package opentarget

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
)

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
