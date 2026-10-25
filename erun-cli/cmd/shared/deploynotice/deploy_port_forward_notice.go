package deploynotice

import (
	"github.com/sophium/erun/cmd/shared/portfwd"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

// NoticeStaleRuntimePortForwards warns when a rollout left local port-forwards bound to the
// replaced pod. deploy does not own forwards, so it names the repair rather than restarting them.
func NoticeStaleRuntimePortForwards(ctx core.Context, specs []helmdeploy.DeploySpec) {
	if ctx.DryRun {
		return
	}
	for _, spec := range specs {
		if spec.SkipHelm || spec.Deploy.ReleaseName != config.RuntimeReleaseName(spec.Target.Tenant) {
			continue
		}
		if !runtimeMCPPortForwardIsStale(spec.Target.Tenant, spec.Target.Environment) {
			continue
		}
		ctx.Info("warning: the local port-forwards for " + spec.Target.Tenant + "/" + spec.Target.Environment + " still point at the replaced runtime pod")
		ctx.Info("    re-establish them with: erun open --tenant " + spec.Target.Tenant + " --environment " + spec.Target.Environment + " --no-shell --no-alias-prompt")
		return
	}
}

// runtimeMCPPortForwardIsStale reports whether a tracked MCP port-forward no longer answers.
func runtimeMCPPortForwardIsStale(tenant, environment string) bool {
	statePath, err := portfwd.McpPortForwardStatePath(tenant, environment, false)
	if err != nil {
		return false
	}
	state, err := portfwd.LoadMCPPortForwardState(statePath)
	if err != nil || state.LocalPort <= 0 {
		return false
	}
	if state.Tenant != tenant || state.Environment != environment {
		return false
	}
	return !portfwd.CanReachLocalMCPEndpoint(state.LocalPort)
}
