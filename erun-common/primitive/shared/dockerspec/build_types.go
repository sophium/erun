package dockerspec

import (
	"errors"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/helm"
)

var (
	ErrDockerBuildContextNotFound = errors.New("dockerfile not found in current directory")
	ErrLinuxPackageBuildNotFound  = errors.New("linux package Dockerfile not found in current directory")
	MultiPlatformDockerBuilds     = []string{"linux/amd64", "linux/arm64"}
)

type DockerBuildSpec struct {
	ContextDir     string
	DockerfilePath string
	Image          model.DockerImageReference
	Platforms      []string
	Push           bool
	Verbosity      int
	// Fingerprint hashes the Dockerfile and its COPY sources.
	Fingerprint string
	// Promote re-tags and pushes an existing matching image instead of building.
	Promote bool
	// MissingFingerprintPlatforms uses "" for a single-platform build.
	MissingFingerprintPlatforms []string
	// CascadeRebuildFromTag names the rebuilding FROM dependency that forces this
	// rebuild despite a matching fingerprint.
	CascadeRebuildFromTag string
	// LocalBaseTag is set when the base exists only under per-arch local tags,
	// so ERUN_VERSION must name the arch being built.
	LocalBaseTag string
	// DindCPULimit / DindMemoryLimitMiB stay empty unless the Dockerfile
	// declares the matching ARG.
	DindCPULimit       string
	DindMemoryLimitMiB string
	// CgroupParent nests RUN containers under an enforced CPU quota instead of
	// a sibling of the dind sidecar's cgroup.
	CgroupParent string
	// DockerSecrets holds references only, never credential values.
	DockerSecrets []config.DockerBuildSecret
	// PlatformObserver reports per-platform timing without changing
	// DockerImageBuilderFunc's signature.
	PlatformObserver func(platform string, elapsed time.Duration, err error, cgroup *model.BuildCgroupMetrics, buildOutput string) `json:"-"`
	// Cache lets the builder correct a "cache hit" when a promote falls back to
	// a real build; nil without a timing root.
	Cache *core.CacheDecision
}

type DockerPushSpec struct {
	Dir       string
	Image     model.DockerImageReference
	Verbosity int
}

type ScriptSpec struct {
	Dir  string
	Path string
	Env  []string
}

type BuildExecutionSpec struct {
	Release       *model.ReleaseSpec
	Script        *ScriptSpec
	LinuxPackages []model.LinuxPackageSpec
	DockerBuilds  []DockerBuildSpec
	DockerPushes  []DockerPushSpec
	// ComponentCharts are build sources independent of images.
	ComponentCharts []helm.HelmChartPublishSpec
}

type DockerPushExecutionSpec struct {
	Builds          []DockerBuildSpec
	Pushes          []DockerPushSpec
	ComponentCharts []helm.HelmChartPublishSpec
}

type LinuxPackageContext struct {
	Dir            string
	DockerfilePath string
}
