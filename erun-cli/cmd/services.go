package cmd

import (
	"fmt"
	"sort"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newServicesCmd(store common.ExposeStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services TENANT ENVIRONMENT",
		Short: "List an environment's Kubernetes Services, noting which are already exposed",
		Long: "Lists every Service running in the environment's namespace, so `erun expose` has a real Service to " +
			"point at instead of a name the operator has to already know. A Service already reachable at a public " +
			"hostname (a real `expose-*` Ingress routes to it) is reported with that hostname; otherwise this names " +
			"the logical label `erun expose` would use to route back to it (the Service's name with the tenant's " +
			"resource prefix stripped), or nothing when the Service's name doesn't carry that prefix -- expose has " +
			"no way to route to it correctly yet. Read-only: two `kubectl get` calls, never a mutation.",
		Example: "  erun services team dev\n" +
			"  erun services team dev --output json",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesCommand(withCloudContextPreflight(commandContext(cmd), store), store, args[0], args[1])
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runServicesCommand(ctx common.Context, store common.ExposeStore, tenant, environment string) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParams{
		Tenant:            tenant,
		Environment:       environment,
		Namespace:         common.KubernetesNamespaceName(tenant, environment),
		KubernetesContext: strings.TrimSpace(envConfig.KubernetesContext),
	}
	if err := ctx.RequireKubernetesContext(req.KubernetesContext); err != nil {
		return err
	}
	services, err := common.ListEnvironmentServices(ctx, req, tenant)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(services)
	}
	return writeServicesResult(ctx, services)
}

func writeServicesResult(ctx common.Context, services []common.EnvironmentService) error {
	sorted := make([]common.EnvironmentService, len(services))
	copy(sorted, services)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	if _, err := fmt.Fprintf(ctx.Stdout, "Services (%d):\n", len(sorted)); err != nil {
		return err
	}
	for _, svc := range sorted {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: ports %s\n", svc.Name, formatServicePorts(svc.Ports)); err != nil {
			return err
		}
		var status string
		switch {
		case svc.Exposed:
			status = fmt.Sprintf("exposed at %s://%s", svc.Scheme, svc.Hostname)
		case svc.ExposableLabel != "":
			status = fmt.Sprintf("not exposed (erun expose <tenant> <environment> %s routes here)", svc.ExposableLabel)
		default:
			status = "not exposed, and not exposable yet: this Service's name doesn't carry the tenant's resource prefix, so erun expose has no Service to route to"
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "    %s\n", status); err != nil {
			return err
		}
	}
	return nil
}

func formatServicePorts(ports []common.EnvironmentServicePort) string {
	if len(ports) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		label := fmt.Sprintf("%d", port.Port)
		if port.Protocol != "" {
			label += "/" + port.Protocol
		}
		if port.Name != "" {
			label = port.Name + ":" + label
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
