package helm

type CommandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

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
