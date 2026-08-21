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
	var servicesZone string
	var platformNamespace string
	var dns01TokenFile string
	var dns01BrokerURL string
	var acmeEmail string
	var acmeServer string
	var dns01WebhookGroupName string
	cmd := &cobra.Command{
		Use:   "expose TENANT ENVIRONMENT SERVICE",
		Short: "Expose an environment's Service at a public HTTPS hostname",
		Long: "Expose an in-namespace Service at a stable public hostname under the platform's services zone.\n\n" +
			"SERVICE is the logical service name: it becomes the hostname label and routes to the tenant-scoped " +
			"in-namespace Service <tenant>-<service> (the name its component chart renders, e.g. `api` -> `frs-api`), " +
			"so the public host stays a clean label (api.frs-prod.services.erunpaas.com) while the Ingress targets the " +
			"real Service. Ensures the per-environment wildcard DNS record points at the env's ingress IP and applies a " +
			"Host-routing Ingress for the Service. TLS is on by default: the Ingress references the env's per-env " +
			"wildcard cert Secret (<tenant>-<env>-wildcard-tls). Nothing populates that Secret unless --dns01-token-file, " +
			"--dns01-broker-url, and --acme-email are all set, in which case expose also provisions a namespaced " +
			"cert-manager Issuer + Certificate through erun's DNS-01 broker so the certificate actually issues; " +
			"omitting them still applies the Ingress, it just serves the cluster's default certificate until something " +
			"else populates the Secret. Pass --no-tls for http. Requires a platform block in .erun/config.yaml " +
			"unless --services-zone and --platform-namespace are both set; it mutates the platform DNS zone and " +
			"applies to the env's cluster. Use --dry-run to preview the actions.",
		Example: "  erun expose team dev api --ip 127.0.0.1\n" +
			"  erun expose team prod api --ip 203.0.113.10 --port 8080\n" +
			"  erun expose team dev api --ip 127.0.0.1 --no-tls\n" +
			"  erun expose team dev api --ip 127.0.0.1 --services-zone services.example.com --platform-namespace frs-prod\n" +
			"  erun expose team dev api --ip 127.0.0.1 --dns01-token-file token.txt --dns01-broker-url https://api.example.com/v1/dns01 --acme-email admin@example.com",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExposeCommand(withCloudContextPreflight(commandContext(cmd), store), store, findProjectRoot, exposeCommandArgs{
				tenant: args[0], environment: args[1], service: args[2],
				targetIP: targetIP, servicePort: servicePort, noTLS: noTLS, ingressClass: ingressClass, tlsSecret: tlsSecret,
				skipIfUnconfigured: skipIfUnconfigured, servicesZone: servicesZone, platformNamespace: platformNamespace,
				dns01TokenFile: dns01TokenFile, dns01BrokerURL: dns01BrokerURL, acmeEmail: acmeEmail, acmeServer: acmeServer,
				dns01WebhookGroupName: dns01WebhookGroupName,
			})
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&targetIP, "ip", "", "Ingress IP the per-env wildcard record points at (e.g. 127.0.0.1 for a local cluster, the public LB IP for remote)")
	cmd.Flags().IntVar(&servicePort, "port", 0, "Service port to route to (default 80)")
	cmd.Flags().BoolVar(&noTLS, "no-tls", false, "Serve http instead of https (skip the tls block on the Ingress)")
	cmd.Flags().StringVar(&ingressClass, "ingress-class", "", "Ingress controller class (default traefik)")
	cmd.Flags().StringVar(&tlsSecret, "tls-secret", "", "Override the per-env wildcard cert Secret name (default <tenant>-<env>-wildcard-tls)")
	cmd.Flags().BoolVar(&skipIfUnconfigured, "skip-if-unconfigured", false, "Succeed as a no-op instead of failing when the project declares no platform block (for scripted callers composing expose after another command)")
	cmd.Flags().StringVar(&servicesZone, "services-zone", "", "Override the platform services zone tenant hostnames live under, so expose needs no project checkout (requires --platform-namespace too)")
	cmd.Flags().StringVar(&platformNamespace, "platform-namespace", "", "Override the namespace running the platform's PowerDNS singleton, so expose needs no project checkout (requires --services-zone too)")
	cmd.Flags().StringVar(&dns01TokenFile, "dns01-token-file", "", "Path to a file holding the per-env DNS-01 broker token; with --dns01-broker-url and --acme-email, provisions a namespaced cert-manager Issuer + Certificate so the wildcard TLS Secret actually gets populated")
	cmd.Flags().StringVar(&dns01BrokerURL, "dns01-broker-url", "", "Base URL of the DNS-01 broker the cluster's cert-manager webhook shim forwards challenges to (requires --dns01-token-file and --acme-email)")
	cmd.Flags().StringVar(&acmeEmail, "acme-email", "", "ACME account contact email for the provisioned per-env certificate (requires --dns01-token-file and --dns01-broker-url)")
	cmd.Flags().StringVar(&acmeServer, "acme-server", "", "ACME directory URL for the provisioned per-env certificate (default Let's Encrypt production)")
	cmd.Flags().StringVar(&dns01WebhookGroupName, "dns01-webhook-group-name", "", "API group the cluster's cert-manager DNS-01 webhook shim registers under (default acme.erun.io)")
	return cmd
}

// exposeCommandArgs bundles runExposeCommand's growing input set — TLS/DNS-01
// cert provisioning added five related flags at once (#1093), past the point
// a flat parameter list stays readable.
type exposeCommandArgs struct {
	tenant, environment, service                                                 string
	targetIP                                                                     string
	servicePort                                                                  int
	noTLS                                                                        bool
	ingressClass, tlsSecret                                                      string
	skipIfUnconfigured                                                           bool
	servicesZone, platformNamespace                                              string
	dns01TokenFile, dns01BrokerURL, acmeEmail, acmeServer, dns01WebhookGroupName string
}

func runExposeCommand(ctx common.Context, store common.ExposeStore, findProjectRoot common.ProjectFinderFunc, a exposeCommandArgs) error {
	servicesZone := strings.TrimSpace(a.servicesZone)
	platformNamespace := strings.TrimSpace(a.platformNamespace)
	// --services-zone/--platform-namespace supply what a project checkout would
	// otherwise resolve, precisely so a caller with no checkout at all (the
	// hosted deploy Job, which has no git repo to find — #1086) can still run
	// expose. Skip the project lookup entirely in that case, rather than failing
	// on it before RunExposeService even gets a chance to use the override.
	projectRoot := ""
	if servicesZone == "" && platformNamespace == "" {
		if findProjectRoot == nil {
			findProjectRoot = common.FindProjectRoot
		}
		_, root, err := findProjectRoot()
		if err != nil {
			// --skip-if-unconfigured exists to make expose a no-op when the
			// target is not set up for exposure — no project at all is the
			// strongest case of that, not a reason to fail before
			// RunExposeService gets to decide (#1086). Fall through with an
			// empty ProjectRoot: its platform-configured check on "" resolves
			// to false, same as a project with no platform block.
			if !a.skipIfUnconfigured {
				return err
			}
		} else {
			projectRoot = root
		}
	}
	result, err := common.RunExposeService(ctx, common.ExposeServiceParams{
		Tenant:             strings.TrimSpace(a.tenant),
		Environment:        strings.TrimSpace(a.environment),
		Service:            strings.TrimSpace(a.service),
		ProjectRoot:        projectRoot,
		TargetIP:           strings.TrimSpace(a.targetIP),
		ServicePort:        a.servicePort,
		NoTLS:              a.noTLS,
		IngressClass:       strings.TrimSpace(a.ingressClass),
		TLSSecretName:      strings.TrimSpace(a.tlsSecret),
		SkipIfUnconfigured: a.skipIfUnconfigured,
		ServicesZone:       servicesZone,
		PlatformNamespace:  platformNamespace,
		TLS: common.TLSCertParams{
			DNS01TokenPath:        strings.TrimSpace(a.dns01TokenFile),
			DNS01BrokerURL:        strings.TrimSpace(a.dns01BrokerURL),
			DNS01WebhookGroupName: strings.TrimSpace(a.dns01WebhookGroupName),
			ACMEEmail:             strings.TrimSpace(a.acmeEmail),
			ACMEServer:            strings.TrimSpace(a.acmeServer),
		},
	}, store, nil, nil)
	if err != nil {
		return err
	}
	if !ctx.DryRun && result.Hostname != "" {
		_, _ = fmt.Fprintf(ctx.Stdout, "exposed %s/%s service %s at %s://%s\n", result.Tenant, result.Environment, result.Service, result.Scheme, result.Hostname)
	}
	return nil
}
