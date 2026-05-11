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
	// BaseVersion is the stable semver without any snapshot suffix (e.g. "1.0.51").
	// Set only for local snapshot builds where Version differs from BaseVersion.
	// Used as the ERUN_VERSION build arg and as an additional local tag so that
	// downstream images (FROM …:${ERUN_VERSION}) can resolve from the local Docker
	// cache instead of pulling from the registry.
	BaseVersion         string
	Tag                 string
	IsLocalBuild        bool
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
	// Fingerprint is a content hash over the Dockerfile and every COPY source
	// resolved against ContextDir, honoring .dockerignore. Set during incremental
	// resolution. After a successful build, the image is locally tagged with
	// fingerprintTag(...) so subsequent builds with the same Fingerprint can
	// skip the build and promote (re-tag + push) instead.
	Fingerprint string
	// Promote indicates the build should be skipped because a local image already
	// exists at fingerprintTag(...) for this Fingerprint. Instead of running
	// docker build, the executor re-tags the existing fp-tagged image as the
	// target tag (and per-platform tags + manifest assembly for multi-platform)
	// and pushes it.
	Promote bool
	// MissingFingerprintPlatforms lists platforms whose fp-tag was absent from
	// the local Docker store when applyIncrementalPromotion ran. Empty when
	// all expected fp-tags were present or when incremental did not run.
	// Used by traceIncrementalDecision to explain why a build is rebuilding.
	// For non-multi-platform builds the slot is the empty string.
	MissingFingerprintPlatforms []string
	// CascadeRebuildFromTag is set when this build's own fingerprint matched
	// (its fp-tags were present) but a local FROM dependency is rebuilding,
	// forcing this build to rebuild too. The value is the dependency's image
	// tag. Captured so the trace can say "rebuilding because <dep> is
	// rebuilding" instead of the misleading "fingerprint image is missing".
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
	// NoIncremental disables the default fingerprint-based incremental build
	// caching. When false (the default), each docker build context is fingerprinted
	// from its Dockerfile and COPY sources; if a local image with the matching
	// fingerprint tag already exists, the build is skipped and the existing image
	// is re-tagged and pushed instead of being rebuilt.
	NoIncremental bool
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
