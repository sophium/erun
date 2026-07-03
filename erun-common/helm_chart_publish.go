package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HelmChartPublishSpec describes a chart package-then-push step. It packages
// into the chart's parent directory rather than a temp dir so the dry-run trace
// shows the real helm argv and working directory.
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

// RunHelmChartPublish traces and executes the chart package-then-push pair.
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
	defer func() { _ = os.Remove(tgzPath) }()
	if err := runHelmCommand(ctx, push); err != nil {
		return fmt.Errorf("helm push %s: %w", spec.ChartName, err)
	}
	return nil
}

// VerifyPublishedHelmChart re-pulls the just-pushed chart so a release never
// assumes remote state: the artifact later steps and consuming envs depend on
// must be provably fetchable.
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
	defer func() { _ = os.Remove(filepath.Join(destination, chartName+"-"+version+".tgz")) }()
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
		return "", fmt.Errorf("missing name field in Chart.yaml at %s", chartPath)
	}
	return name, nil
}

// helmChartDeclaresDependencies flags an umbrella chart that vendors published
// subcharts into charts/ (the erun-blueprint-platform pattern): deploy must run
// `helm dependency build` on it first, since helm upgrade --install fails when
// the declared subcharts are missing from charts/.
func helmChartDeclaresDependencies(chartPath string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return false, fmt.Errorf("read Chart.yaml at %s: %w", chartPath, err)
	}
	var chart struct {
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return false, fmt.Errorf("parse Chart.yaml at %s: %w", chartPath, err)
	}
	return len(chart.Dependencies) > 0, nil
}

// helmChartRuntimeSubchartKey returns the value-scope key of a local runtime
// umbrella's wrapped erun-devops subchart — its dependency alias, else its name
// — or "" when the chart declares no erun-devops dependency (a forked top-level
// runtime chart, or not a runtime chart at all). erun deploy nests the runtime
// --sets under this key so a wrapped erun-devops receives its wiring.
func helmChartRuntimeSubchartKey(chartPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return "", fmt.Errorf("read Chart.yaml at %s: %w", chartPath, err)
	}
	var chart struct {
		Dependencies []struct {
			Name  string `yaml:"name"`
			Alias string `yaml:"alias"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return "", fmt.Errorf("parse Chart.yaml at %s: %w", chartPath, err)
	}
	for _, dep := range chart.Dependencies {
		if strings.TrimSpace(dep.Name) != DevopsComponentName {
			continue
		}
		if alias := strings.TrimSpace(dep.Alias); alias != "" {
			return alias, nil
		}
		return DevopsComponentName, nil
	}
	return "", nil
}

// resolveHelmChartPublishSpec keeps every erun chart under the registry's
// /charts path so a chart never collides with its component's same-named image
// repo at the same ref.
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
		return HelmChartPublishSpec{}, fmt.Errorf("publish %s: chart version is required", chartName)
	}
	ociRepo := PublishedDevopsChartOCIRepo(registry)
	return HelmChartPublishSpec{
		ChartPath: chartPath,
		ChartName: chartName,
		Version:   resolvedVersion,
		OCIRepo:   ociRepo,
	}, nil
}

// publishComponentChart publishes the Helm chart that ships with a component
// image so every component is deployable by envs and platform wrapper charts,
// not just the runtime erun-devops chart.
//
// chartVersion is intentionally decoupled from image.Version: version-pinned
// bases (e.g. erun-powerdns at upstream 4.9.3, erun-backend-postgres at 18.3)
// keep their image at the upstream pin but must still publish their chart at the
// release version so platform deploys resolve it. It is a no-op for an image
// that ships no chart (a tenant's <tenant>-devops override, or infra like dind).
func publishComponentChart(ctx Context, image DockerImageReference, chartVersion string) error {
	imageName := strings.TrimSpace(image.ImageName)
	projectRoot := strings.TrimSpace(image.ProjectRoot)
	if imageName == "" || projectRoot == "" {
		return nil
	}
	chartPath, err := findComponentHelmChartPath(projectRoot, imageName)
	if err != nil {
		// No chart ships with this image; nothing to publish.
		return nil
	}
	version := strings.TrimSpace(chartVersion)
	if version == "" {
		version = strings.TrimSpace(image.Version)
	}
	publish, err := resolveHelmChartPublishSpec(chartPath, version, image.Registry)
	if err != nil {
		return err
	}
	publish.Verbosity = ctx.Verbosity
	if err := RunHelmChartPublish(ctx, publish); err != nil {
		return err
	}
	return VerifyPublishedHelmChart(ctx, publish.OCIRepo, publish.ChartName, publish.Version)
}
