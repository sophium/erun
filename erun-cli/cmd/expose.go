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
	var noTLS bool
	var ingressClass string
	var tlsSecret string
	var skipIfUnconfigured bool
	cmd := &cobra.Command{
		Use:   "expose TENANT ENVIRONMENT SERVICE",
		Short: "Expose an environment's Service at a public HTTPS hostname",
		Long: "Expose an in-namespace Service at a stable public hostname under the platform's services zone.\n\n" +
			"SERVICE is the logical service name: it becomes the hostname label and routes to the tenant-scoped " +
			"in-namespace Service <tenant>-<service> (the name its component chart renders, e.g. `api` -> `frs-api`), " +
			"so the public host stays a clean label (api.frs-prod.services.erunpaas.com) while the Ingress targets the " +
			"real Service. Ensures the per-environment wildcard DNS record points at the env's ingress IP and applies a " +
			"Host-routing Ingress for the Service. TLS is on by default: the Ingress references the env's per-env " +
			"wildcard cert Secret (<tenant>-<env>-wildcard-tls, issued by the cluster edge), so the host serves https " +
			"with no per-service cert step. Pass --no-tls for http. Requires a platform block in .erun/config.yaml; it " +
			"mutates the platform DNS zone and applies to the env's cluster. Use --dry-run to preview the actions.",
		Example: "  erun expose team dev api --ip 127.0.0.1\n" +
			"  erun expose team prod api --ip 203.0.113.10 --port 8080\n" +
			"  erun expose team dev api --ip 127.0.0.1 --no-tls",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExposeCommand(withCloudContextPreflight(commandContext(cmd), store), store, findProjectRoot, args[0], args[1], args[2], targetIP, servicePort, noTLS, ingressClass, tlsSecret, skipIfUnconfigured)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&targetIP, "ip", "", "Ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)")
	cmd.Flags().IntVar(&servicePort, "port", 0, "Service port to route to (default 80)")
	cmd.Flags().BoolVar(&noTLS, "no-tls", false, "Serve http instead of https (skip the tls block on the Ingress)")
	cmd.Flags().StringVar(&ingressClass, "ingress-class", "", "Ingress controller class (default traefik)")
	cmd.Flags().StringVar(&tlsSecret, "tls-secret", "", "Override the per-env wildcard cert Secret name (default <tenant>-<env>-wildcard-tls)")
	cmd.Flags().BoolVar(&skipIfUnconfigured, "skip-if-unconfigured", false, "Succeed as a no-op instead of failing when the project declares no platform block (for scripted callers composing expose after another command)")
	return cmd
}

func runExposeCommand(ctx common.Context, store common.ExposeStore, findProjectRoot common.ProjectFinderFunc, tenant, environment, service, targetIP string, servicePort int, noTLS bool, ingressClass, tlsSecret string, skipIfUnconfigured bool) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := common.RunExposeService(ctx, common.ExposeServiceParams{
		Tenant:             strings.TrimSpace(tenant),
		Environment:        strings.TrimSpace(environment),
		Service:            strings.TrimSpace(service),
		ProjectRoot:        projectRoot,
		TargetIP:           strings.TrimSpace(targetIP),
		ServicePort:        servicePort,
		NoTLS:              noTLS,
		IngressClass:       strings.TrimSpace(ingressClass),
		TLSSecretName:      strings.TrimSpace(tlsSecret),
		SkipIfUnconfigured: skipIfUnconfigured,
	}, store, nil, nil)
	if err != nil {
		return err
	}
	if !ctx.DryRun && result.Hostname != "" {
		_, _ = fmt.Fprintf(ctx.Stdout, "exposed %s/%s service %s at %s://%s\n", result.Tenant, result.Environment, result.Service, result.Scheme, result.Hostname)
	}
	return nil
}
