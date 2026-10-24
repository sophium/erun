package shared

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// RunCommandCapturingOutput keeps a verification read quiet on success — the
// manifest body is not what the operator asked for — while still surfacing the
// tool's own diagnostics and capturing everything for failure classification.
// See commandOutputCapture for why stdout and stderr need separate buffers.
func RunCommandCapturingOutput(ctx core.Context, spec helm.CommandSpec) (string, error) {
	capture := &core.CommandOutputCapture{}
	cmd := core.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = &capture.Stdout
	cmd.Stderr = core.TeeWriter(ctx.Stderr, &capture.Stderr)
	err := cmd.Run()
	return capture.Combined(), err
}
