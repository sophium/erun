package runcmd

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// SimpleJobExecute adapts a plain RunRuntimeCommand call to RunJobEnvelope's execute shape.
func SimpleJobExecute(runtime runtimecfg.RuntimeConfig, verbosity int, run func(core.Context, string) error) func(bool, io.Writer) (toolio.CommandOutput, error) {
	return func(preview bool, log io.Writer) (toolio.CommandOutput, error) {
		return RunRuntimeCommand(runtime, preview, verbosity, log, run)
	}
}
