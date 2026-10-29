package model

type DockerImageReference struct {
	ProjectRoot string
	Environment string
	Registry    string
	ImageName   string
	Version     string
	// BaseVersion is the non-snapshot version for local snapshot builds, so
	// FROM …:${ERUN_VERSION} resolves from the local cache.
	BaseVersion         string
	Tag                 string
	VersionFilePath     string
	VersionFromBuildDir bool
	// Insecure marks Registry as plain HTTP; `docker manifest` ignores the daemon's
	// insecure list, so callers must pass --insecure themselves.
	Insecure bool
}

type DockerBuildContext struct {
	Dir            string
	DockerfilePath string
}

type DockerCommandTarget struct {
	ProjectRoot     string
	Environment     string
	VersionOverride string
	Release         bool
	Force           bool
	Deploy          bool
	// Build is CLI orchestration policy; the shared resolvers never read it.
	Build                       bool
	NoIncremental               bool
	DisableBuildScriptDiscovery bool
	// Platforms must be empty for a release, which always publishes every platform.
	Platforms []string
	// Component selects a components: entry; empty auto-selects a lone entry.
	Component string
}

type DockerRegistryAuthError struct {
	Tag      string
	Registry string
	Message  string
	Err      error
}

type DockerPushSpec struct {
	Dir       string
	Image     DockerImageReference
	Verbosity int
}
