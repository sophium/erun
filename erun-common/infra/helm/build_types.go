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
