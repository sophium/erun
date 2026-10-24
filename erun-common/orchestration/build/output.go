package build

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// NewBuildResult extracts the structured result from a resolved build execution.
func NewBuildResult(execution shared.BuildExecutionSpec) core.BuildResult {
	result := core.BuildResult{}
	if execution.Release != nil {
		result.Version = strings.TrimSpace(execution.Release.Version)
	}
	for _, build := range execution.DockerBuilds {
		if result.Version == "" {
			result.Version = strings.TrimSpace(build.Image.Version)
			result.BaseVersion = strings.TrimSpace(build.Image.BaseVersion)
		}
		result.Images = append(result.Images, core.BuildResultImage{
			Name: strings.TrimSpace(build.Image.ImageName),
			Tag:  strings.TrimSpace(build.Image.Tag),
		})
	}
	for _, chart := range execution.ComponentCharts {
		result.Charts = append(result.Charts, core.BuildResultChart{
			Name:    strings.TrimSpace(chart.ChartName),
			Version: strings.TrimSpace(chart.Version),
		})
	}
	if execution.Script != nil {
		result.Scripts = append(result.Scripts, filepath.Join(execution.Script.Dir, execution.Script.Path))
	}
	for _, pkg := range execution.LinuxPackages {
		result.Packages = append(result.Packages, pkg.Name)
	}
	if result.BaseVersion == result.Version {
		result.BaseVersion = ""
	}
	return result
}
