package config

// DockerBuildSecret is a BuildKit secret declared under `docker.secrets`. It holds only a
// reference (env var name or host path), never the value, so the credential cannot leak.
type DockerBuildSecret struct {
	// ID is the id the Dockerfile mounts with `RUN --mount=type=secret,id=<ID>`.
	ID string `yaml:"id"`
	// Env names the variable docker reads the credential from.
	Env string `yaml:"env,omitempty"`
	// Src names a host file or directory holding the credential.
	Src string `yaml:"src,omitempty"`
}
