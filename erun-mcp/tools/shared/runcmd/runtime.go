package runcmd

import (
	"bytes"
	"io"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/e2e"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// RunRuntimeCommand runs one tool's work and captures it; log is the background
// job's log, or nil for a synchronous call.
func RunRuntimeCommand(runtime runtimecfg.RuntimeConfig, preview bool, verbosity int, log io.Writer, run func(core.Context, string) error) (toolio.CommandOutput, error) {
	traceOutput := new(bytes.Buffer)
	traceSink := toolio.MirrorToJobLog(traceOutput, log)
	ctx := toolio.RuntimeCallContext(preview, verbosity, nil, traceSink, traceSink)
	ctx.KubernetesContextPreflight = cloud.CloudContextPreflight(runtime.Store, cloud.CloudContextDependencies{})
	// The env trace feeds the desktop's Diagnostics console; read-only tools bypass
	// RunRuntimeCommand so probe polls don't flood it.
	ctx, closeEnvTrace := e2e.ActivateEnvTrace(ctx, runtime.Context.Tenant, runtime.Context.Environment)
	defer closeEnvTrace()

	workDir, err := toolio.RuntimeRepoPath(runtime.Context)
	if err != nil {
		return toolio.CommandOutput{}, err
	}

	output, err := toolio.RunCommandOutput(ctx, workDir, traceOutput, log, func(runCtx core.Context) error {
		return run(runCtx, workDir)
	})
	return output, err
}
