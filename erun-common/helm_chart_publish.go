package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

