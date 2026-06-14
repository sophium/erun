package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HelmChartPublishSpec describes a single chart-publish step: package the
// chart at ChartPath, then push the resulting tgz to OCIRepo as an OCI Helm
// artifact. The published reference is `<OCIRepo trimmed of oci://>/<ChartName>:<Version>`.
//
// The publish step packages into the chart's parent directory rather than a
// temp dir so the dry-run trace shows the actual `helm package` and
// `helm push` argv the real run would execute, including the working
// directory. The tgz is cleaned up after push.
type HelmChartPublishSpec struct {
	ChartPath string
	ChartName string
	Version   string
	OCIRepo   string
	Verbosity int
}

func (p HelmChartPublishSpec) packageCommand() commandSpec {
	return commandSpec{
		Dir:  filepath.Dir(p.ChartPath),
		Name: "helm",
		Args: []string{
			"package",
			filepath.Base(p.ChartPath),
			"--version", p.Version,
			"--app-version", p.Version,
		},
	}
}

func (p HelmChartPublishSpec) pushCommand() commandSpec {
	return commandSpec{
		Dir:  filepath.Dir(p.ChartPath),
		Name: "helm",
		Args: []string{
			"push",
			p.ChartName + "-" + p.Version + ".tgz",
			p.OCIRepo,
		},
	}
}

// RunHelmChartPublish traces and executes the package + push pair. In
// dry-run the trace lines are emitted and execution short-circuits before
// any filesystem or network side effects occur. Real-run packages into the
// chart's parent directory and removes the tgz once push completes (or
// fails), regardless of the push outcome.
func RunHelmChartPublish(ctx Context, spec HelmChartPublishSpec) error {
	if strings.TrimSpace(spec.ChartName) == "" {
		return errors.New("publish: chart name is required")
	}
	if strings.TrimSpace(spec.Version) == "" {
		return fmt.Errorf("publish %s: chart version is required", spec.ChartName)
	}
	if strings.TrimSpace(spec.OCIRepo) == "" {
		return fmt.Errorf("publish %s: oci repo is required", spec.ChartName)
	}

	pkg := spec.packageCommand()
	push := spec.pushCommand()
	ctx.TraceCommand(pkg.Dir, pkg.Name, pkg.Args...)
	ctx.TraceCommand(push.Dir, push.Name, push.Args...)
	if ctx.DryRun {
		return nil
	}

	ctx.Info("==> Publishing " + spec.ChartName + " " + spec.Version + " to " + spec.OCIRepo)
	if err := runHelmCommand(ctx, pkg); err != nil {
		return fmt.Errorf("helm package %s: %w", spec.ChartName, err)
	}
	tgzPath := filepath.Join(pkg.Dir, spec.ChartName+"-"+spec.Version+".tgz")
	defer os.Remove(tgzPath)
	if err := runHelmCommand(ctx, push); err != nil {
		return fmt.Errorf("helm push %s: %w", spec.ChartName, err)
	}
	return nil
}

// VerifyPublishedHelmChart pulls the just-pushed chart back from the OCI
// registry into a temp directory, proving the artifact later steps (and
// every env consuming the published chart) depend on is actually fetchable.
// Release Rules: do not assume remote state after pushing — check it.
func VerifyPublishedHelmChart(ctx Context, ociRepo, chartName, version string) error {
	destination := filepath.Join(os.TempDir(), "erun-chart-verify")
	args := []string{
		"pull",
		strings.TrimSuffix(strings.TrimSpace(ociRepo), "/") + "/" + chartName,
		"--version", version,
		"--destination", destination,
	}
	ctx.TraceCommand("", "helm", args...)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	if err := runHelmCommand(ctx, commandSpec{Name: "helm", Args: args}); err != nil {
		return fmt.Errorf("verify published chart %s:%s: %w", chartName, version, err)
	}
	defer os.Remove(filepath.Join(destination, chartName+"-"+version+".tgz"))
	ctx.Info("==> Verified published chart " + chartName + " " + version)
	return nil
}

func runHelmCommand(ctx Context, spec commandSpec) error {
	cmd := Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

// loadHelmChartName reads the `name` field from <chartPath>/Chart.yaml.
// Callers use this to know the tgz filename `helm package` will produce so
// `helm push` can reference it without globbing the destination dir.
func loadHelmChartName(chartPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return "", err
	}
	var chart struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return "", fmt.Errorf("parse Chart.yaml at %s: %w", chartPath, err)
	}
	name := strings.TrimSpace(chart.Name)
	if name == "" {
		return "", fmt.Errorf("Chart.yaml at %s is missing the name field", chartPath)
	}
	return name, nil
}

// resolveHelmChartPublishSpec builds a HelmChartPublishSpec from a resolved
// HelmDeploySpec and the chart path. It fails fast on a missing container
// registry rather than surfacing the error in the middle of a deploy.
func resolveHelmChartPublishSpec(chartPath, version, containerRegistry string) (HelmChartPublishSpec, error) {
	chartName, err := loadHelmChartName(chartPath)
	if err != nil {
		return HelmChartPublishSpec{}, err
	}
	registry := strings.TrimSpace(containerRegistry)
	if registry == "" {
		return HelmChartPublishSpec{}, fmt.Errorf("publish %s: container registry is required (mark a registry with the deploy role in .erun/config.yaml)", chartName)
	}
	resolvedVersion := strings.TrimSpace(version)
	if resolvedVersion == "" {
		return HelmChartPublishSpec{}, fmt.Errorf("publish %s: chart version is required (pass --version or persist runtimeversion in env config)", chartName)
	}
	// The runtime chart and its image share a name; deploy pulls the chart from
	// the registry's /charts path (PublishedDevopsChartOCIRepo) so its tag space
	// stays separate from the image repo. Publish it there — matching the
	// release path and where every published-chart env pulls from — so a pushed
	// version is actually deployable. Other charts publish under the registry
	// root unchanged.
	ociRepo := "oci://" + registry
	if chartName == DevopsComponentName {
		ociRepo = PublishedDevopsChartOCIRepo(registry)
	}
	return HelmChartPublishSpec{
		ChartPath: chartPath,
		ChartName: chartName,
		Version:   resolvedVersion,
		OCIRepo:   ociRepo,
	}, nil
}
