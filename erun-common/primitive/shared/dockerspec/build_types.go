package dockerspec

import (
	"errors"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/helm"
)

var (
	ErrDockerBuildContextNotFound = errors.New("dockerfile not found in current directory")
	ErrLinuxPackageBuildNotFound  = errors.New("linux package Dockerfile not found in current directory")
	MultiPlatformDockerBuilds     = []string{"linux/amd64", "linux/arm64"}
)

type ScriptSpec struct {
	Dir  string
	Path string
	Env  []string
}

type BuildExecutionSpec struct {
	Release       *model.ReleaseSpec
	Script        *ScriptSpec
	LinuxPackages []model.LinuxPackageSpec
	DockerBuilds  []seam.DockerBuildSpec
	DockerPushes  []model.DockerPushSpec
	// ComponentCharts are build sources independent of images.
	ComponentCharts []helm.HelmChartPublishSpec
}

type DockerPushExecutionSpec struct {
	Builds          []seam.DockerBuildSpec
	Pushes          []model.DockerPushSpec
	ComponentCharts []helm.HelmChartPublishSpec
}

type LinuxPackageContext struct {
	Dir            string
	DockerfilePath string
}
