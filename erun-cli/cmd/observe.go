package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newObserveCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment string
	var secretChecks []string
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Report an environment's Kubernetes state, read-only",
		Long: "Report pods (each container's name, running image, and resource limits),\n" +
			"ResourceQuota/LimitRange usage, Ingress hosts and TLS secret names,\n" +
			"Certificate readiness, and the runtime helm release for the environment's\n" +
			"namespace.\n\n" +
			"When a Certificate is not Ready, its CertificateRequest -> Order -> Challenge\n" +
			"chain is walked automatically, so the reason issuance is stuck (for example a\n" +
			"webhook solver's RBAC denial) comes back in this one call instead of three more.\n\n" +
			"The runtime helm release is reported with its chart/app version and the values\n" +
			"erun itself sets (image overrides, runtime pod resource limits), and diffed\n" +
			"against both the running containers and the env config's recorded\n" +
			"runtimeversion/runtimeimage/runtimepod, so a disagreement (a hand-patched\n" +
			"image, a resized pod the release never recorded) is named instead of left for\n" +
			"the reader to spot by comparing two dumps.\n\n" +
			"Every call is a kubectl get or a helm status: nothing here can mutate the\n" +
			"cluster, which is what makes this safe to grant an orchestrator that must\n" +
			"never reach for `exec raw`.",
		Example: "  erun observe --tenant team --environment dev\n" +
			"  erun observe --tenant team --environment dev --secret db-credentials=password --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks, err := parseObserveSecretChecks(secretChecks)
			if err != nil {
				return err
			}
			return runObserveCommand(commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment), checks)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().StringArrayVar(&secretChecks, "secret", nil, "Check a Secret for a key's presence, as name=key (repeatable); the value is never read")
	return cmd
}

func parseObserveSecretChecks(raw []string) ([]common.ObserveSecretCheck, error) {
	checks := make([]common.ObserveSecretCheck, 0, len(raw))
	for _, entry := range raw {
		name, key, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		key = strings.TrimSpace(key)
		if !found || name == "" || key == "" {
			return nil, fmt.Errorf("--secret must be name=key, got %q", entry)
		}
		checks = append(checks, common.ObserveSecretCheck{Name: name, Key: key})
	}
	return checks, nil
}

func runObserveCommand(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams, checks []common.ObserveSecretCheck) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)
	observed, err := common.RunObservation(ctx, req, common.ObserveParams{Secrets: checks})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(observed)
	}
	return writeObserveResult(ctx, observed)
}
