package config

// ExecutionConfig holds per-operation overrides; anything but "library" means subprocess.
type ExecutionConfig struct {
	Modes map[string]string `yaml:"modes,omitempty" json:"modes,omitempty"`
}
