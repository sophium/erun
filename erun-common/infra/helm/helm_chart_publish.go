package helm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"gopkg.in/yaml.v3"
)

// HelmChartPublishSpec packages into the chart's parent dir so the dry-run trace shows the real argv and cwd.
type HelmChartPublishSpec struct {
	ChartPath string
	ChartName string
	Version   string
	OCIRepo   string
	Verbosity int
}

func (p HelmChartPublishSpec) packageCommand() CommandSpec {
	return CommandSpec{
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

func (p HelmChartPublishSpec) pushCommand() CommandSpec {
	return CommandSpec{
		Dir:  filepath.Dir(p.ChartPath),
		Name: "helm",
		Args: []string{
			"push",
			p.ChartName + "-" + p.Version + ".tgz",
			p.OCIRepo,
		},
	}
}

// dependencyBuildCommand vendors an umbrella chart's subcharts into charts/.
func (p HelmChartPublishSpec) dependencyBuildCommand() CommandSpec {
	return CommandSpec{
		Name: "helm",
		Args: []string{"dependency", "build", p.ChartPath},
	}
}

func (p HelmChartPublishSpec) tgzPath() string {
	return filepath.Join(filepath.Dir(p.ChartPath), p.ChartName+"-"+p.Version+".tgz")
}

func traceChartPackage(ctx core.Context, spec HelmChartPublishSpec, declaresDeps bool) {
	if declaresDeps {
		dep := spec.dependencyBuildCommand()
		ctx.TraceCommand(dep.Dir, dep.Name, dep.Args...)
	}
	pkg := spec.packageCommand()
	ctx.TraceCommand(pkg.Dir, pkg.Name, pkg.Args...)
}

func runChartPackage(ctx core.Context, spec HelmChartPublishSpec, declaresDeps bool) error {
	if declaresDeps {
		// Remove the vendored charts/ to keep the tree clean; build --release requires it.
		chartsDir := filepath.Join(spec.ChartPath, "charts")
		if _, err := os.Stat(chartsDir); os.IsNotExist(err) {
			defer func() { _ = os.RemoveAll(chartsDir) }()
		}
		if err := RunHelmCommand(ctx, spec.dependencyBuildCommand()); err != nil {
			return fmt.Errorf("helm dependency build %s: %w", spec.ChartName, err)
		}
	}
	if err := RunHelmCommand(ctx, spec.packageCommand()); err != nil {
		return fmt.Errorf("helm package %s: %w", spec.ChartName, err)
	}
	return nil
}

// PackageResolvedChart packages a chart to validate it, then removes the .tgz; pushing
// is RunHelmChartPublish's job.
func PackageResolvedChart(ctx core.Context, spec HelmChartPublishSpec) error {
	spec.Verbosity = ctx.Verbosity
	declaresDeps, err := HelmChartDeclaresDependencies(spec.ChartPath)
	if err != nil {
		return err
	}
	traceChartPackage(ctx, spec, declaresDeps)
	if ctx.DryRun {
		return nil
	}
	ctx.Info("==> Packaging " + spec.ChartName + " " + spec.Version)
	if err := runChartPackage(ctx, spec, declaresDeps); err != nil {
		return err
	}
	_ = os.Remove(spec.tgzPath())
	return nil
}

// RunHelmChartPublish runs dependency build (for umbrellas), package, and push.
func RunHelmChartPublish(ctx core.Context, spec HelmChartPublishSpec) error {
	if strings.TrimSpace(spec.ChartName) == "" {
		return errors.New("publish: chart name is required")
	}
	if strings.TrimSpace(spec.Version) == "" {
		return fmt.Errorf("publish %s: chart version is required", spec.ChartName)
	}
	if strings.TrimSpace(spec.OCIRepo) == "" {
		return fmt.Errorf("publish %s: oci repo is required", spec.ChartName)
	}

	declaresDeps, err := HelmChartDeclaresDependencies(spec.ChartPath)
	if err != nil {
		return err
	}
	push := spec.pushCommand()
	traceChartPackage(ctx, spec, declaresDeps)
	ctx.TraceCommand(push.Dir, push.Name, push.Args...)
	if ctx.DryRun {
		return nil
	}

	ctx.Info("==> Publishing " + spec.ChartName + " " + spec.Version + " to " + spec.OCIRepo)
	if err := runChartPackage(ctx, spec, declaresDeps); err != nil {
		return err
	}
	defer func() { _ = os.Remove(spec.tgzPath()) }()
	if err := RunHelmCommand(ctx, push); err != nil {
		return fmt.Errorf("helm push %s: %w", spec.ChartName, err)
	}
	return nil
}

// RunHelmCommand sends both helm streams to stderr so progress never precedes --output json.
func RunHelmCommand(ctx core.Context, spec CommandSpec) error {
	cmd := core.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = ctx.Stderr
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

// RunHelmCommandCapturingOutput streams helm output and also returns it for failure classification.
func RunHelmCommandCapturingOutput(ctx core.Context, spec CommandSpec) (string, error) {
	capture := &core.CommandOutputCapture{}
	cmd := core.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = core.TeeWriter(ctx.Stderr, &capture.Stdout)
	cmd.Stderr = core.TeeWriter(ctx.Stderr, &capture.Stderr)
	err := cmd.Run()
	return capture.Combined(), err
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

// HelmChartDeclaresDependencies reports an umbrella chart needing `helm dependency build` before install.
func HelmChartDeclaresDependencies(chartPath string) (bool, error) {
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

// HelmChartRuntimeSubchartKey returns the erun-devops subchart's alias or name, or "" when
// none is declared; deploy nests runtime --sets under it.
func HelmChartRuntimeSubchartKey(chartPath string) (string, error) {
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
		if strings.TrimSpace(dep.Name) != config.DevopsComponentName {
			continue
		}
		if alias := strings.TrimSpace(dep.Alias); alias != "" {
			return alias, nil
		}
		return config.DevopsComponentName, nil
	}
	return "", nil
}

// ResolveHelmChartPublishSpec puts charts under /charts so they never collide with a same-named image repo.
func ResolveHelmChartPublishSpec(chartPath, version, containerRegistry string) (HelmChartPublishSpec, error) {
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

func shippedComponentChartPath(projectRoot, imageName string) (string, bool) {
	chartPath, err := FindComponentHelmChartPath(projectRoot, imageName)
	return chartPath, err == nil
}

// PublishComponentChart publishes the chart shipped with a component image. chartVersion
// is separate from image.Version because version-pinned bases keep their upstream image tag.
func PublishComponentChart(ctx core.Context, image DockerImageReference, chartVersion string) error {
	imageName := strings.TrimSpace(image.ImageName)
	projectRoot := strings.TrimSpace(image.ProjectRoot)
	if imageName == "" || projectRoot == "" {
		return nil
	}
	chartPath, ok := shippedComponentChartPath(projectRoot, imageName)
	if !ok {
		return nil
	}
	version := strings.TrimSpace(chartVersion)
	if version == "" {
		version = strings.TrimSpace(image.Version)
	}
	publish, err := ResolveHelmChartPublishSpec(chartPath, version, image.Registry)
	if err != nil {
		return err
	}
	publish.Verbosity = ctx.Verbosity
	if err := RunHelmChartPublish(ctx, publish); err != nil {
		return err
	}
	return VerifyPublishedHelmChart(ctx, publish.OCIRepo, publish.ChartName, publish.Version)
}
