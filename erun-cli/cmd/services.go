package cmd

import (
	"fmt"
	"strconv"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newServicesCmd builds `erun services`: the CLI's own surface for the read
// model the desktop's Ports tab renders as its Service picker
// (eruncommon.ListEnvironmentServices). It exists because that call shipped on
// the desktop alone, which left `erun expose`'s SERVICE argument -- a derived
// label that resolves to the in-namespace Service <tenant>-<service> -- a name
// the caller had to know in advance, with nothing on this surface able to
// report it and no way to name the real backend when the derivation is wrong.
func newServicesCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment string
	cmd := &cobra.Command{
		Use:   "services",
		Short: "List the Services an environment runs, and which are already exposed",
		Long: "Report every Service in the environment's namespace with its type, its\n" +
			"ports, and the public hostname it already has when an erun-expose Ingress\n" +
			"fronts it.\n\n" +
			"This answers the question that precedes `erun expose`: what is this\n" +
			"environment running, and which of those is already published. The exposure\n" +
			"is read from the Ingress's own backend rather than re-derived from the\n" +
			"`<tenant>-<service>` convention, so a repo-native chart that names its\n" +
			"Service something else still reports the Service its Ingress actually routes\n" +
			"to.\n\n" +
			"Both reads are plain `kubectl get`s: nothing here can mutate the cluster,\n" +
			"which is what makes it safe to grant to an orchestrator that must never\n" +
			"reach for `exec raw`.",
		Example: "  erun services --tenant team --environment dev\n" +
			"  erun services --tenant team --environment dev --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesCommand(commandContext(cmd), resolveOpen, scopedOpenParams(cmd.CommandPath(), tenant, environment))
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	return cmd
}

func runServicesCommand(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)
	services, err := common.RunListEnvironmentServices(ctx, req)
	if err != nil {
		return fmt.Errorf("cannot list this environment's Services: %w", err)
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(services)
	}
	return writeServicesResult(ctx, services)
}

// writeServicesResult renders the listing as the report it is. An exposed
// Service carries its public address on an indented line beneath it, the same
// shape `erun observe` uses for an Ingress's backends: the Service line stays
// about what is running, and the address is what the exposure added to it.
func writeServicesResult(ctx common.Context, result common.EnvironmentServiceList) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Namespace: %s\n", result.Namespace); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Services (%d):\n", len(result.Services)); err != nil {
		return err
	}
	if len(result.Services) == 0 {
		_, err := fmt.Fprintf(ctx.Stdout, "  none -- this environment is running no Services yet\n")
		return err
	}
	for _, service := range result.Services {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s (%s): ports %s\n",
			service.Name, valueOrNone(service.Type), formatServicePorts(service.Ports)); err != nil {
			return err
		}
		if service.Exposure == nil {
			continue
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "    exposed as %s at %s://%s\n",
			service.Exposure.Label, service.Exposure.Scheme, service.Exposure.Hostname); err != nil {
			return err
		}
	}
	return nil
}

// formatServicePorts renders the ports an Ingress would route to, named the
// way the Service names them and bare where kubectl reports none -- a
// single-port Service is the common case and the reason a picker cannot rely
// on names alone.
func formatServicePorts(ports []common.ObservedServicePort) string {
	rendered := make([]string, 0, len(ports))
	for _, port := range ports {
		if strings.TrimSpace(port.Name) != "" {
			rendered = append(rendered, fmt.Sprintf("%s:%d", port.Name, port.Port))
			continue
		}
		rendered = append(rendered, strconv.Itoa(port.Port))
	}
	return formatStringList(rendered)
}
