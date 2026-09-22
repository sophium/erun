package cmd

import (
	"context"
	"fmt"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newResizeCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, deployHelmChart common.HelmChartDeployerFunc, resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment, cpu, memory, dindCPU, dindMemory, orchestrator string
	var applyRecommendation, overrideLease bool
	cmd := &cobra.Command{
		Use:   "resize",
		Short: "Change the runtime pod's or erun-dind sidecar's CPU/memory limits and roll it out",
		Long: "Change the runtime container's own CPU/memory limits — the throttle/OOM ceiling\n" +
			"and the namespace ResourceQuota draw those limits count against — without\n" +
			"re-running `erun init` to change two numbers. Pass --cpu and/or --memory\n" +
			"explicitly, or --apply-recommendation to size from the environment's own\n" +
			"standing sizing recommendation instead of retyping a value the product already\n" +
			"computed. --dind-cpu/--dind-memory resize the erun-dind sidecar instead — the\n" +
			"container that actually runs `erun build`/`erun release` — and may be combined\n" +
			"with the runtime-pod flags in the same call; apply-recommendation never sizes\n" +
			"the sidecar, since it has no standing recommendation of its own (see --help on\n" +
			"the sidecar flags). A resize whose resolved size already matches the current\n" +
			"one is a no-op that says so.\n\n" +
			"A resize rolls the runtime pod (Recreate strategy), which kills any live agent\n" +
			"session inside it, so it first checks the environment's activity leases and\n" +
			"refuses, naming the holder, when the environment is not idle; pass\n" +
			"--override-lease to roll it anyway.\n\n" +
			"--apply-recommendation needs retained usage history, which is only readable\n" +
			"from inside the environment's own runtime pod (this command run there, or its\n" +
			"MCP resize tool) — a host-side laptop/desktop invocation has no trend to read\n" +
			"and refuses rather than guessing. This does not resize the scheduler's request\n" +
			"(a small fixed value, unaffected by this command) or any PVC.",
		Example: "  erun resize --tenant team --environment dev --cpu 6 --memory 12Gi\n" +
			"  erun resize --tenant team --environment dev --dind-memory 16Gi\n" +
			"  erun resize --tenant team --environment dev --apply-recommendation\n" +
			"  erun resize --tenant team --environment dev --apply-recommendation --dry-run\n" +
			"  erun resize --tenant team --environment dev --cpu 6 --override-lease",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := withCloudContextPreflight(commandContext(cmd), store)
			result, err := common.RunRuntimeResize(ctx, common.RuntimeResizeDependencies{
				Store:                          store,
				SaveEnvConfig:                  saveEnvConfig,
				FindProjectRoot:                findProjectRoot,
				ResolveDockerBuildContext:      resolveBuildContext,
				ResolveKubernetesDeployContext: resolveDeployContext,
				Now:                            time.Now,
				DeployHelmChart:                deployHelmChart,
				LoadActivityLeases:             resizeActivityLeaseLoader(cmd.Context(), ctx, resolveOpen),
			}, common.RuntimeResizeParams{
				Tenant:      tenant,
				Environment: environment,
				Input: common.RuntimeResizeInput{
					CPU:                 cpu,
					Memory:              memory,
					DindCPU:             dindCPU,
					DindMemory:          dindMemory,
					ApplyRecommendation: applyRecommendation,
				},
				OverrideLease: overrideLease,
				Holder:        common.EnvironmentActivityLeaseHolder{Orchestrator: orchestrator},
			})
			if err != nil {
				return err
			}
			if ctx.Output == common.OutputJSON {
				return ctx.WriteResult(result)
			}
			return writeResizeResult(ctx, result)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().StringVar(&cpu, "cpu", "", "Explicit CPU limit for the runtime pod (Kubernetes quantity, e.g. 6); omit to leave CPU unchanged unless --apply-recommendation")
	cmd.Flags().StringVar(&memory, "memory", "", "Explicit memory limit for the runtime pod (Kubernetes quantity, e.g. 12Gi); omit to leave memory unchanged unless --apply-recommendation")
	cmd.Flags().StringVar(&dindCPU, "dind-cpu", "", "Explicit CPU limit for the erun-dind sidecar (Kubernetes quantity, e.g. 6); omit to leave it unchanged")
	cmd.Flags().StringVar(&dindMemory, "dind-memory", "", "Explicit memory limit for the erun-dind sidecar (Kubernetes quantity, e.g. 16Gi); omit to leave it unchanged. Raise this when a multi-arch erun release/erun build --release OOMs inside the sidecar")
	cmd.Flags().BoolVar(&applyRecommendation, "apply-recommendation", false, "Size the runtime pod from the environment's own standing sizing recommendation instead of --cpu/--memory (never sizes the erun-dind sidecar)")
	cmd.Flags().BoolVar(&overrideLease, "override-lease", false, "Roll the runtime pod even though the environment is currently held by another worker")
	cmd.Flags().StringVar(&orchestrator, "orchestrator", "", "The calling orchestrator's own id, recorded on the resize's lease and on the override if one was needed")
	return cmd
}

// resizeActivityLeaseLoader gives RunRuntimeResize's occupancy check a lease
// reader that actually reaches the target environment. LoadEnvironmentActivityLeases
// reads whatever filesystem the calling process itself runs on; a resize invoked
// from the operator's own machine against a remote environment runs on a
// different filesystem than that environment's own lease store (the same
// "two homes" split environment_call.go documents for activity/job/idle), so
// reading locally there always finds nothing and the guard never fires. When
// this process is not itself the target environment, dispatch through the
// environment's own MCP edge instead, exactly like `erun activity lease list`
// already does off-environment.
//
// The dispatch runs even under --dry-run, unlike callEnvironmentTool's normal
// dry-run behavior of only tracing the call: resize's own dry-run exists to
// answer "would this be refused", and a stale "no leases" answer would silently
// defeat that. Reading leases has no side effect, so forcing the real call here
// costs nothing dry-run is meant to avoid.
func resizeActivityLeaseLoader(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver) func(string, string, time.Time) ([]common.EnvironmentActivityLease, error) {
	probeCtx := commandCtx
	probeCtx.DryRun = false
	return func(tenant, environment string, now time.Time) ([]common.EnvironmentActivityLease, error) {
		if environmentTargetsItself() {
			return common.LoadEnvironmentActivityLeases(tenant, environment, now)
		}
		leases, _, err := listLeasesInEnvironment(ctx, probeCtx, resolveOpen, tenant, environment)
		return leases, err
	}
}

func writeResizeResult(ctx common.Context, result common.RuntimeResizeResult) error {
	if ctx.DryRun {
		return nil
	}
	if result.Plan.NoOp {
		_, err := fmt.Fprintf(ctx.Stdout, "%s/%s is already sized at cpu=%s memory=%s dind-cpu=%s dind-memory=%s; no change\n", result.Plan.Tenant, result.Plan.Environment, result.Plan.Current.CPU, result.Plan.Current.Memory, result.Plan.DindCurrent.CPU, result.Plan.DindCurrent.Memory)
		return err
	}
	for _, action := range result.Plan.Actions {
		if _, err := fmt.Fprintf(ctx.Stdout, "%s: %s -> %s\n", action.Resource, action.From, action.To); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "==> Resized %s/%s\n", result.Plan.Tenant, result.Plan.Environment)
	return err
}
