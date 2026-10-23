package cmd

import (
	"fmt"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// reportExecutionModes answers "it behaves differently on my machine" for the
// subprocess/library execution switch (see erun-common/execution_mode.go):
// without this, an operator who flipped execution.modes.<operation> to
// "library" in config.yaml has no way to confirm it actually took effect
// short of reading the config file by hand. Root config, not
// per-environment, so it runs unconditionally — it needs no runtime pod and
// nothing here can fail dry-run.
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
