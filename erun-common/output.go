package eruncommon

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// NewBuildResult extracts the structured result from a resolved build execution.
func NewBuildResult(execution BuildExecutionSpec) core.BuildResult {
	result := core.BuildResult{}
	if execution.release != nil {
		result.Version = strings.TrimSpace(execution.release.Version)
	}
	for _, build := range execution.dockerBuilds {
		if result.Version == "" {
			result.Version = strings.TrimSpace(build.Image.Version)
			result.BaseVersion = strings.TrimSpace(build.Image.BaseVersion)
		}
		result.Images = append(result.Images, core.BuildResultImage{
			Name: strings.TrimSpace(build.Image.ImageName),
			Tag:  strings.TrimSpace(build.Image.Tag),
		})
	}
	for _, chart := range execution.componentCharts {
		result.Charts = append(result.Charts, core.BuildResultChart{
			Name:    strings.TrimSpace(chart.ChartName),
			Version: strings.TrimSpace(chart.Version),
		})
	}
	if execution.script != nil {
		result.Scripts = append(result.Scripts, filepath.Join(execution.script.Dir, execution.script.Path))
	}
	for _, pkg := range execution.linuxPackages {
		result.Packages = append(result.Packages, pkg.Name)
	}
	if result.BaseVersion == result.Version {
		result.BaseVersion = ""
	}
	return result
}
