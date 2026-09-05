package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newUnexposeCmd(store common.ExposeStore, cloudStore common.CloudReadStore, deps common.CloudDependencies, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var skipIfUnconfigured bool
	var servicesZone string
	var platformNamespace string
	var erunAlias string
	cmd := &cobra.Command{
		Use:   "unexpose TENANT ENVIRONMENT",
		Short: "Remove an environment's per-env wildcard DNS record",
		Long: "Removes the per-env wildcard DNS record `erun expose` created — the DNS-side counterpart to that " +
			"primitive, run at environment teardown so records don't accumulate for environments that no longer " +
			"exist and a later environment reusing the same name doesn't inherit a stale one. It touches only the " +
			"platform DNS zone; the Ingress that referenced the record lives in the environment's own namespace and " +
			"is torn down with it, so unexpose has nothing to do there. Requires a platform block in .erun/config.yaml " +
			"unless --services-zone and --platform-namespace are both set. Uses the same direct-pdnsutil-or-platform-api " +
			"path selection `erun expose` does. Use --dry-run to preview the action.",
		Example: "  erun unexpose team dev\n" +
			"  erun unexpose team dev --services-zone services.example.com --platform-namespace frs-prod\n" +
			"  erun unexpose team dev --erun-alias prod",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnexposeCommand(withCloudContextPreflight(commandContext(cmd), store), store, cloudStore, deps, findProjectRoot, args[0], args[1], skipIfUnconfigured, servicesZone, platformNamespace, erunAlias)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&skipIfUnconfigured, "skip-if-unconfigured", false, "Succeed as a no-op instead of failing when the project declares no platform block (for scripted callers composing unexpose after another command)")
	cmd.Flags().StringVar(&servicesZone, "services-zone", "", "Override the platform services zone tenant hostnames live under, so unexpose needs no project checkout (requires --platform-namespace too)")
	cmd.Flags().StringVar(&platformNamespace, "platform-namespace", "", "Override the namespace running the platform's PowerDNS singleton, so unexpose needs no project checkout (requires --services-zone too)")
	cmd.Flags().StringVar(&erunAlias, "erun-alias", "", "erun platform cloud alias to route the DNS delete through when direct PowerDNS access is unavailable (defaults to the sole configured erun-type alias; only needed to disambiguate when more than one is configured)")
	return cmd
}

func runUnexposeCommand(ctx common.Context, store common.ExposeStore, cloudStore common.CloudReadStore, deps common.CloudDependencies, findProjectRoot common.ProjectFinderFunc, tenant, environment string, skipIfUnconfigured bool, servicesZone, platformNamespace, erunAlias string) error {
	servicesZone = strings.TrimSpace(servicesZone)
	platformNamespace = strings.TrimSpace(platformNamespace)
	// --services-zone/--platform-namespace supply what a project checkout would
	// otherwise resolve, precisely so a caller with no checkout at all (the
	// hosted delete Job, which has no git repo to find — mirroring the expose
	// side) can still run unexpose. Skip the project lookup entirely in
	// that case, rather than failing on it before RunUnexposeService even gets
	// a chance to use the override.
	projectRoot := ""
	if servicesZone == "" && platformNamespace == "" {
		if findProjectRoot == nil {
			findProjectRoot = common.FindProjectRoot
		}
		_, root, err := findProjectRoot()
		if err != nil {
			if !skipIfUnconfigured {
				return err
			}
		} else {
			projectRoot = root
		}
	}
	result, err := common.RunUnexposeService(ctx, common.UnexposeParams{
		Tenant:             strings.TrimSpace(tenant),
		Environment:        strings.TrimSpace(environment),
		ProjectRoot:        projectRoot,
		SkipIfUnconfigured: skipIfUnconfigured,
		ServicesZone:       servicesZone,
		PlatformNamespace:  platformNamespace,
		ErunAlias:          strings.TrimSpace(erunAlias),
	}, store, cloudStore, deps, nil)
	if err != nil {
		return err
	}
	if !ctx.DryRun && result.WildcardName != "" {
		_, _ = fmt.Fprintf(ctx.Stdout, "removed wildcard DNS record %s for %s/%s\n", result.WildcardName, result.Tenant, result.Environment)
	}
	return nil
}
