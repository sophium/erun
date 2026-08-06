package cmd

import (
	common "github.com/sophium/erun/erun-common"
)

// noticeStaleRuntimePortForwards tells the operator when a runtime rollout left
// the environment's local port-forwards pointing at the replaced pod.
//
// `kubectl port-forward` binds one pod, so a rollout orphans the forward: the
// local socket keeps listening while every request fails, which reads as "the
// environment is broken" rather than "reconnect me". deploy does not own
// port-forward lifecycle — `erun open` starts, adopts, and tracks the forwards —
// so this reports the condition and names the command that repairs it instead of
// silently starting background processes for an env the operator never opened.
func noticeStaleRuntimePortForwards(ctx common.Context, specs []common.DeploySpec) {
	if ctx.DryRun {
		return
	}
	for _, spec := range specs {
		if spec.SkipHelm || spec.Deploy.ReleaseName != common.RuntimeReleaseName(spec.Target.Tenant) {
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

// runtimeMCPPortForwardIsStale reports whether this env has a tracked MCP
// port-forward that no longer answers. An env with no tracked forward was never
// opened on this host, so there is nothing to repair.
func runtimeMCPPortForwardIsStale(tenant, environment string) bool {
	statePath, err := mcpPortForwardStatePath(tenant, environment)
	if err != nil {
		return false
	}
	state, err := loadMCPPortForwardState(statePath)
	if err != nil || state.LocalPort <= 0 {
		return false
	}
	if state.Tenant != tenant || state.Environment != environment {
		return false
	}
	return !canReachLocalMCPEndpoint(state.LocalPort)
}
