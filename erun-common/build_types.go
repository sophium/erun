package eruncommon

import (
	"errors"
	"io"
	"time"
)

const localSnapshotTimestampFormat = "20060102150405"

var (
	ErrVersionFileNotFound        = errors.New("version file not found for current module")
	ErrDockerBuildContextNotFound = errors.New("dockerfile not found in current directory")
	ErrLinuxPackageBuildNotFound  = errors.New("linux package build script not found in current directory")
	multiPlatformDockerBuilds     = []string{"linux/amd64", "linux/arm64"}
)

type commandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type (
	BuildContextResolverFunc func() (DockerBuildContext, error)
	NowFunc                  func() time.Time
	DockerImageBuilderFunc   func(DockerBuildSpec, io.Writer, io.Writer) error
	DockerImagePusherFunc    func(string, int, io.Writer, io.Writer) error
	DockerImageInspectorFunc func(string) (bool, error)
	DockerRegistryLoginFunc  func(string, io.Reader, io.Writer, io.Writer) error
	BuildScriptRunnerFunc    func(string, string, []string, io.Reader, io.Writer, io.Writer) error
	DockerPushFunc           func(Context, DockerPushSpec) error
)

type DockerStore interface {
	ListTenantConfigs() ([]TenantConfig, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

type DockerBuildContext struct {
	Dir            string
	DockerfilePath string
}

type DockerImageReference struct {
	ProjectRoot string
	Environment string
	Registry    string
	ImageName   string
	Version     string
	// BaseVersion is the stable semver without the snapshot suffix, set only for
	// local snapshot builds where it differs from Version. It exists so downstream
	// images (FROM …:${ERUN_VERSION}) resolve from the local Docker cache instead
	// of the registry.
	BaseVersion         string
	Tag                 string
	VersionFilePath     string
	VersionFromBuildDir bool
	// Insecure marks Registry as plain HTTP (a cluster registry with
	// `insecure: true`). `docker manifest` never consults the daemon's
	// insecure-registry list, so anything that shells out to it for this
	// image must pass its own `--insecure` explicitly.
	Insecure bool
}

type DockerBuildSpec struct {
	ContextDir     string
	DockerfilePath string
	Image          DockerImageReference
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
	// ARGs, so an in-build gate (the erun-devops runtime image's own `make
	// check`) can size its concurrent fan-out against what this build's dind
	// sidecar is actually entitled to, instead of the Dockerfile's own
	// hardcoded ARG default or the host node's raw, cgroup-invisible capacity
	// (erun-devops/AGENTS.md's dind cgroup blind-spot notes). Left empty for a
	// Dockerfile that declares neither ARG, so an unrelated build's docker
	// command is unchanged. See applyDindResourceBuildArgs.
	DindCPULimit       string
	DindMemoryLimitMiB string
	// PlaywrightTestAreas carries the smoke+area selection resolved from the
	// Playwright spec-file diff against the merge base (see
	// applyPlaywrightAreaBuildArgs) into a Dockerfile that declares a matching
	// PLAYWRIGHT_TEST_AREAS ARG, so the in-build gate runs only the areas whose
	// specs changed instead of the full suite on every build. Left empty for a
	// Dockerfile that declares no such ARG, or when the selection could not be
	// resolved (no git repo, no merge base) -- the Dockerfile's own ARG default
	// then runs the full suite, the fail-safe direction.
	PlaywrightTestAreas string
	// CgroupParent names the cgroup every RUN-instruction container this build
	// creates should nest under, so it inherits a real, enforced CPU quota
	// instead of escaping the erun-dind sidecar's own kubelet-declared limit as
	// a sibling cgroup (erun#2255). Left empty outside an injected runtime pod.
	// See buildContainerCPUCapCgroupParent.
	CgroupParent string
	// PlatformObserver, when set, is called after each platform's build (or
	// promote+push) finishes, reporting that platform's elapsed time, error,
	// and build-cgroup cost (nil for a promote, which runs no docker build).
	// It lets a caller attach per-architecture timing (see Context.
	// timingPlatformObserver in timing.go) without DockerImageBuilderFunc
	// needing a signature change, since executeDockerBuild sets this field on
	// the same buildInput value it hands to the builder — exactly how it already
	// threads Verbosity through. Never marshaled: a func value has no JSON form.
	PlatformObserver func(platform string, elapsed time.Duration, err error, cgroup *BuildCgroupMetrics) `json:"-"`
}

type DockerPushSpec struct {
	Dir       string
	Image     DockerImageReference
	Verbosity int
}

type scriptSpec struct {
	Dir  string
	Path string
	Env  []string
}

type BuildExecutionSpec struct {
	release      *ReleaseSpec
	script       *scriptSpec
	linuxBuilds  []scriptSpec
	dockerBuilds []DockerBuildSpec
	dockerPushes []DockerPushSpec
	// componentCharts are the Helm charts under <tenant>-devops/k8s/*, resolved as
	// first-class build source independent of images. A plain build packages them
	// (validate); a build that pushes publishes them.
	componentCharts []HelmChartPublishSpec
	skippedLinux    bool
}

type DockerPushExecutionSpec struct {
	builds []DockerBuildSpec
	pushes []DockerPushSpec
	// componentCharts are the <tenant>-devops/k8s/* charts this push publishes,
	// discovered by directory scan rather than keyed to same-named images.
	componentCharts []HelmChartPublishSpec
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
	// configured environments.<env>.docker.platforms. It must be empty when
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
	Dir               string
	BuildScriptPath   string
	ReleaseScriptPath string
}
