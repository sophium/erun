package config

// DockerBuildSecret is one BuildKit build secret a project declares under
// `docker.secrets` in .erun/config.yaml, resolved for the build's environment.
//
// It carries a *reference* to the credential — the name of an environment
// variable, or a host path — and never the credential itself. That is the
// property that keeps a secret out of every surface erun writes: the value is
// never in this struct, so it cannot reach the docker build argv as a literal,
// a trace line, a log, or a timing record. docker resolves it from its own
// (inherited) environment, or reads it from the file it is handed.
type DockerBuildSecret struct {
	// ID is the secret id the Dockerfile mounts, as in
	// `RUN --mount=type=secret,id=<ID>`.
	ID string `yaml:"id"`
	// Env names the environment variable holding the credential, rendered as
	// `--secret id=<ID>,env=<Env>`. docker reads the value from its own
	// environment, so it never enters this process.
	Env string `yaml:"env,omitempty"`
	// Src names a host file or directory holding the credential, rendered as
	// `--secret id=<ID>,src=<Src>`.
	Src string `yaml:"src,omitempty"`
}
