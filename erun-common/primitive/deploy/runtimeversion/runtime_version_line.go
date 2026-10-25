package runtimeversion

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
)

// ResolveRuntimeVersionLine derives the line from the image a deploy recorded,
// never from naming conventions.
func ResolveRuntimeVersionLine(tenant string, env config.EnvConfig) model.RuntimeVersionLine {
	image := strings.TrimSpace(env.RuntimeRunningImage)
	if image == "" {
		return model.RuntimeVersionLine{Undetermined: true}
	}
	registry, name, _, ok := chartref.ParseDockerImageReference(image)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return model.RuntimeVersionLine{Undetermined: true}
	}
	stock := name == config.DefaultRuntimeImageName
	reference := name
	if registry != "" {
		reference = registry + "/" + name
	}
	return model.RuntimeVersionLine{
		Line:      config.ImageReleaseLine(name),
		Image:     reference,
		Disagrees: stock && runtimetarget.RuntimeReleaseName(tenant) != runtimetarget.DevopsComponentName,
	}
}

// ResolveErunVersion reads config alone and returns nil rather than guess; an
// empty RuntimeChart follows RuntimeVersion only on erun's own line.
func ResolveErunVersion(env config.EnvConfig, runtimeLine *model.RuntimeVersionLine) *model.ErunVersion {
	runtimeVersion := strings.TrimSpace(env.RuntimeVersion)
	if runtimeVersion == "" {
		return nil
	}
	chart := strings.TrimSpace(env.RuntimeChart)
	if chart == "" {
		if runtimeLine == nil || runtimeLine.Line != "erun" {
			return nil
		}
		return &model.ErunVersion{Version: runtimeVersion, SameAsRuntimeVersion: true}
	}
	_, chartVersion := chartref.SplitChartReference(chart)
	chartVersion = strings.TrimSpace(chartVersion)
	if chartVersion == "" {
		return nil
	}
	return &model.ErunVersion{Version: chartVersion}
}
