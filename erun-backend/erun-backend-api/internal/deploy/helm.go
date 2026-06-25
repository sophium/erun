package deploy

import (
	"context"
	"fmt"
	"io"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/client-go/rest"

	eruncommon "github.com/sophium/erun/erun-common"
)

// helmDeploy installs (or upgrades) the runtime chart into namespace on the
// cluster addressed by cfg, entirely in-process via the Helm Go SDK — no `helm`
// subprocess. chartRef is either the published OCI reference
// (oci://<registry>/charts/erun-devops, pinned by version) or a local chart
// directory (the verification seam). It is idempotent: a release that does not
// exist yet is installed, an existing one is upgraded — the in-process
// equivalent of `helm upgrade --install`.
func helmDeploy(ctx context.Context, cfg *rest.Config, releaseName, namespace, chartRef, version string, values map[string]any, wait, plainHTTP bool) error {
	registryOpts := []registry.ClientOption{registry.ClientOptEnableCache(true), registry.ClientOptWriter(io.Discard)}
	if plainHTTP {
		// Verification seam: a local OCI registry (e.g. registry:2) serves plain
		// HTTP. Production pulls the published chart from ghcr over HTTPS, so this
		// stays false there.
		registryOpts = append(registryOpts, registry.ClientOptPlainHTTP())
	}
	registryClient, err := registry.NewClient(registryOpts...)
	if err != nil {
		return fmt.Errorf("helm registry client: %w", err)
	}

	actionConfig := new(action.Configuration)
	getter := &restConfigGetter{cfg: cfg, namespace: namespace}
	if err := actionConfig.Init(getter, namespace, "secret", func(string, ...any) {}); err != nil {
		return fmt.Errorf("helm action init: %w", err)
	}
	// Must be set before NewInstall/NewUpgrade: those snapshot cfg.RegistryClient
	// into the action's chart-path options for oci:// pulls.
	actionConfig.RegistryClient = registryClient

	settings := cli.New()
	timeout, _ := time.ParseDuration(eruncommon.DefaultHelmDeploymentTimeout)

	exists, err := releaseExists(actionConfig, releaseName)
	if err != nil {
		return err
	}
	if !exists {
		return helmInstall(ctx, actionConfig, settings, releaseName, namespace, chartRef, version, values, wait, timeout)
	}
	return helmUpgrade(ctx, actionConfig, settings, releaseName, chartRef, version, values, wait, timeout)
}

// releaseExists reports whether a helm release with this name already has
// history, so the caller chooses install vs upgrade.
func releaseExists(actionConfig *action.Configuration, releaseName string) (bool, error) {
	history := action.NewHistory(actionConfig)
	history.Max = 1
	switch _, err := history.Run(releaseName); err {
	case nil:
		return true, nil
	case driver.ErrReleaseNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("helm history %q: %w", releaseName, err)
	}
}

func helmInstall(ctx context.Context, actionConfig *action.Configuration, settings *cli.EnvSettings, releaseName, namespace, chartRef, version string, values map[string]any, wait bool, timeout time.Duration) error {
	install := action.NewInstall(actionConfig)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.CreateNamespace = true
	install.Version = version
	install.Wait = wait
	install.Timeout = timeout

	chrt, err := loadChart(&install.ChartPathOptions, settings, chartRef)
	if err != nil {
		return err
	}
	if _, err := install.RunWithContext(ctx, chrt, values); err != nil {
		return fmt.Errorf("helm install %q: %w", releaseName, err)
	}
	return nil
}

func helmUpgrade(ctx context.Context, actionConfig *action.Configuration, settings *cli.EnvSettings, releaseName, chartRef, version string, values map[string]any, wait bool, timeout time.Duration) error {
	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Install = true
	upgrade.MaxHistory = 10
	upgrade.Version = version
	upgrade.Wait = wait
	upgrade.Timeout = timeout

	chrt, err := loadChart(&upgrade.ChartPathOptions, settings, chartRef)
	if err != nil {
		return err
	}
	if _, err := upgrade.RunWithContext(ctx, releaseName, chrt, values); err != nil {
		return fmt.Errorf("helm upgrade %q: %w", releaseName, err)
	}
	return nil
}

// loadChart resolves chartRef (an oci:// reference at the pinned version, or a
// local chart directory) to a loaded chart. LocateChart short-circuits to a
// local path when chartRef is a directory, which serves the verification seam.
func loadChart(opts *action.ChartPathOptions, settings *cli.EnvSettings, chartRef string) (*chart.Chart, error) {
	path, err := opts.LocateChart(chartRef, settings)
	if err != nil {
		if eruncommon.IsOCIChartReference(chartRef) {
			// The published chart is pinned by --version; the usual cause of a pull
			// failure is a version whose chart was never published (a version is
			// deployable only after `erun push` publishes its image + chart
			// together). Surface that actionably instead of a bare helm error,
			// mirroring eruncommon.PublishedChartNotFoundError on the CLI path.
			return nil, fmt.Errorf("runtime chart %s version %s could not be pulled — a version is deployable only after `erun push` publishes its chart: %w", chartRef, opts.Version, err)
		}
		return nil, fmt.Errorf("locate chart %q: %w", chartRef, err)
	}
	loaded, err := loader.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load chart %q: %w", chartRef, err)
	}
	return loaded, nil
}
