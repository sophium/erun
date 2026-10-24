package shared

import (
	"errors"
	"io"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

var (
	ErrVersionFileNotFound        = errors.New("version file not found for current module")
	ErrDockerBuildContextNotFound = errors.New("dockerfile not found in current directory")
	ErrLinuxPackageBuildNotFound  = errors.New("linux package Dockerfile not found in current directory")
	MultiPlatformDockerBuilds     = []string{"linux/amd64", "linux/arm64"}
)

type (
	BuildContextResolverFunc func() (DockerBuildContext, error)
	NowFunc                  func() time.Time
	DockerImagePusherFunc    func(string, int, io.Writer, io.Writer) error
)

type DockerStore interface {
	ListTenantConfigs() ([]config.TenantConfig, error)
	LoadTenantConfig(string) (config.TenantConfig, string, error)
	ListEnvConfigs(string) ([]config.EnvConfig, error)
}

type DockerBuildContext struct {
	Dir            string
	DockerfilePath string
}

type DockerBuildSpec struct {
	ContextDir     string
	DockerfilePath string
	Image          helm.DockerImageReference
	Platforms      []string
	Push           bool
	Verbosity      int
	// Fingerprint is a content hash of the Dockerfile and its COPY sources. A
	// matching fingerprint lets a later build skip rebuilding and promote the
	// existing image (re-tag + push) instead.
	Fingerprint string
	// Promote indicates a local image already matches this Fingerprint, so the
	// build is skipped and the existing image is re-tagged and pushed instead of
	// rebuilt.
	Promote bool
	// MissingFingerprintPlatforms lists platforms that lacked a matching
	// fingerprint tag, so the trace can explain why a build is rebuilding rather
	// than promoting. For non-multi-platform builds the slot is the empty string.
	MissingFingerprintPlatforms []string
	// CascadeRebuildFromTag holds a local FROM dependency's image tag when this
	// build must rebuild only because that dependency is rebuilding, even though
	// its own fingerprint matched. It lets the trace name the real cause instead
	// of the misleading "fingerprint image is missing".
	CascadeRebuildFromTag string
	// LocalBaseTag holds the image tag of the `FROM …:${ERUN_VERSION}` base that
	// this same build produces locally, when that base is never published by this
	// run. Such a base only exists under its per-arch local tags, so the wrapper's
	// ERUN_VERSION build arg must name the arch being built or the plain version
	// reference resolves nowhere.
	LocalBaseTag string
	// DindCPULimit / DindMemoryLimitMiB carry this build's actual erun-dind
	// sidecar resource limits (EnvConfig.RuntimeDindPod, normalized) into a
	// Dockerfile that declares matching DIND_CPU_LIMIT / DIND_MEMORY_LIMIT_MIB
	// ARGs, so a component's in-build test stage can size its concurrent
	// fan-out against what this build's dind sidecar is actually entitled to,
	// instead of the Dockerfile's own hardcoded ARG default or the host node's
	// raw, cgroup-invisible capacity. Left empty for a
	// Dockerfile that declares neither ARG, so an unrelated build's docker
	// command is unchanged. See applyDindResourceBuildArgs.
	DindCPULimit       string
	DindMemoryLimitMiB string
	// CgroupParent names the cgroup every RUN-instruction container this build
	// creates should nest under, so it inherits a real, enforced CPU quota
	// instead of escaping the erun-dind sidecar's own kubelet-declared limit as
	// a sibling cgroup (erun#2255). Left empty outside an injected runtime pod.
	// See buildContainerCPUCapCgroupParent.
	CgroupParent string
	// DockerSecrets carries the build secrets declared under `docker.secrets`
	// (resolved for this build's environment) into the docker build argv as
	// `--secret id=<id>,env=<VAR>` / `,src=<path>` references. Each entry holds
	// a reference, never a credential value, which is what keeps a secret out of
	// every trace, log, and timing record this build writes — see
	// DockerBuildSecret.
	DockerSecrets []config.DockerBuildSecret
	// PlatformObserver, when set, is called after each platform's build (or
	// promote+push) finishes, reporting that platform's elapsed time, error,
	// build-cgroup cost (nil for a promote, which runs no docker build), and
	// (when a real `docker build` ran) its captured `--progress=plain`
	// output, which a caller can mine for a per-Dockerfile-step timing
	// breakdown. It lets a caller attach per-architecture timing (see Context.
	// timingPlatformObserver in timing.go) without DockerImageBuilderFunc
	// needing a signature change, since executeDockerBuild sets this field on
	// the same buildInput value it hands to the builder — exactly how it already
	// threads Verbosity through. Never marshaled: a func value has no JSON form.
	PlatformObserver func(platform string, elapsed time.Duration, err error, cgroup *core.BuildCgroupMetrics, buildOutput string) `json:"-"`
	// Cache is the same fingerprint cache decision the timing tree recorded for
	// this image (see cacheDecision), threaded through by value so the builder
	// can correct it: a promote whose cached image cannot be published falls back
	// to a real build, and a row that still said "cache hit" would report the
	// opposite of what the run did. Unexported because it is erun-common's own
	// timing bookkeeping, not an input a caller sets; nil whenever no timing root
	// is active, which is most callers.
	Cache *core.CacheDecision
}

type DockerPushSpec struct {
	Dir       string
	Image     helm.DockerImageReference
	Verbosity int
}

type ScriptSpec struct {
	Dir  string
	Path string
	Env  []string
}

type BuildExecutionSpec struct {
	Release *ReleaseSpec
	Script  *ScriptSpec
	// LinuxPackages are the linux package components a plain build runs
	// through their test stage. A release exports them from its ReleaseSpec.
	LinuxPackages []LinuxPackageSpec
	DockerBuilds  []DockerBuildSpec
	DockerPushes  []DockerPushSpec
	// ComponentCharts are the Helm charts under <tenant>-devops/k8s/*, resolved as
	// first-class build source independent of images. A plain build packages them
	// (validate); a build that pushes publishes them.
	ComponentCharts []helm.HelmChartPublishSpec
}

type DockerPushExecutionSpec struct {
	Builds []DockerBuildSpec
	Pushes []DockerPushSpec
	// ComponentCharts are the <tenant>-devops/k8s/* charts this push publishes,
	// discovered by directory scan rather than keyed to same-named images.
	ComponentCharts []helm.HelmChartPublishSpec
}

type DockerCommandTarget struct {
	ProjectRoot     string
	Environment     string
	VersionOverride string
	Release         bool
	Force           bool
	Deploy          bool
	// Build is the `erun push --build` operator shortcut: build the current source
	// first, then push that version. It is orchestration policy owned by the CLI
	// caller — the shared resolvers never read it. See root AGENTS.md § "Command
	// primitives vs orchestration".
	Build bool
	// NoIncremental disables the default fingerprint-based incremental build cache.
	NoIncremental bool
	// DisableBuildScriptDiscovery skips project build.sh discovery so builds
	// resolve docker/release contexts directly.
	DisableBuildScriptDiscovery bool
	// Platforms explicitly overrides the docker --platform targets a non-release
	// build mints (e.g. ["linux/amd64"]), taking precedence over the project's
	// configured docker.platforms. It must be empty when
	// Release is set: a release build always publishes every platform erun
	// supports, regardless of any override.
	Platforms []string
	// Component selects one components: entry (project_components_config.go) for
	// a monorepo that declares more than one docker/k8s/version root. Empty
	// auto-selects the lone entry when exactly one is declared, or resolves
	// through the project-global paths: block when no components: map exists;
	// more than one entry with Component empty fails naming the choices.
	Component string
}

type DockerRegistryAuthError struct {
	Tag      string
	Registry string
	Message  string
	Err      error
}

type LinuxPackageContext struct {
	Dir            string
	DockerfilePath string
}
