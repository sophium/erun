package doctor

import (
	"fmt"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// reportExecutionModes shows the effective subprocess/library mode per operation.
func reportExecutionModes(ctx core.Context) error {
	config, _, _ := configpkg.LoadERunConfig()
	statuses := configpkg.ExecutionModeReport(config)
	if len(statuses) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "== Execution modes =="); err != nil {
		return err
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(ctx.Stdout, "%s: %s\n", status.Operation, status.Mode); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stdout)
	return err
}
