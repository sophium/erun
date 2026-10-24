package config

// ProjectE2EConfig is the `e2e:` block of a project's .erun/config.yaml.
type ProjectE2EConfig struct {
	// Secrets are kept apart from docker.secrets, which every component build
	// would then require.
	Secrets []DockerBuildSecret `yaml:"secrets,omitempty"`
}

func (c ProjectE2EConfig) IsZero() bool {
	return len(c.Secrets) == 0
}
