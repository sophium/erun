package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newExposeCmd(store common.ExposeStore, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var targetIP string
	var servicePort int
	cmd := &cobra.Command{
		Use:   "expose TENANT ENVIRONMENT SERVICE",
		Short: "Expose an environment's Service at a public hostname",
		Long: "Expose an in-namespace Service at a stable public hostname under the platform's services zone.\n\n" +
			"Ensures the per-environment wildcard DNS record points at the env's ingress IP and applies a " +
			"Host-routing Ingress for the Service. Requires a platform block in .erun/config.yaml; it mutates the " +
			"platform DNS zone and applies to the env's cluster. Use --dry-run to preview the resolved hostname and actions.",
		Example:      "  erun expose team dev api --ip 127.0.0.1\n  erun expose team dev api --ip 203.0.113.10 --port 8080",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExposeCommand(withCloudContextPreflight(commandContext(cmd), store), store, findProjectRoot, args[0], args[1], args[2], targetIP, servicePort)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&targetIP, "ip", "", "Ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)")
	cmd.Flags().IntVar(&servicePort, "port", 0, "Service port to route to (default 80)")
	return cmd
}

func runExposeCommand(ctx common.Context, store common.ExposeStore, findProjectRoot common.ProjectFinderFunc, tenant, environment, service, targetIP string, servicePort int) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := common.RunExposeService(ctx, common.ExposeServiceParams{
		Tenant:      strings.TrimSpace(tenant),
		Environment: strings.TrimSpace(environment),
		Service:     strings.TrimSpace(service),
		ProjectRoot: projectRoot,
		TargetIP:    strings.TrimSpace(targetIP),
		ServicePort: servicePort,
	}, store, nil, nil)
	if err != nil {
		return err
	}
	if !ctx.DryRun {
		_, _ = fmt.Fprintf(ctx.Stdout, "exposed %s/%s service %s at https://%s\n", result.Tenant, result.Environment, result.Service, result.Hostname)
	}
	return nil
}
