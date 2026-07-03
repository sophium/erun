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
	skippedLinux bool
}

type DockerPushExecutionSpec struct {
	builds []DockerBuildSpec
	pushes []DockerPushSpec
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
