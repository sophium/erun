package config

// ProjectE2EConfig is the `e2e:` block of a project's .erun/config.yaml.
type ProjectE2EConfig struct {
	// Secrets are the BuildKit secrets every e2e suite build receives, in the
	// same reference-only shape as docker.secrets. They are deliberately a
	// separate list: docker.secrets reach every component build, so an e2e
	// credential declared there would become mandatory for every build.
	Secrets []DockerBuildSecret `yaml:"secrets,omitempty"`
}

func (c ProjectE2EConfig) IsZero() bool {
	return len(c.Secrets) == 0
}
