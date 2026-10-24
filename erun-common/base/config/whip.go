package config

// WhipConfigOverride is the persisted override of WhipConfig; pointer fields
// distinguish unset from an explicit zero.
type WhipConfigOverride struct {
	Message           *string `yaml:"message,omitempty" json:"message,omitempty"`
	StaleAfterSeconds *int    `yaml:"staleafterseconds,omitempty" json:"staleAfterSeconds,omitempty"`
	MaxNudges         *int    `yaml:"maxnudges,omitempty" json:"maxNudges,omitempty"`
	AutoEnabled       *bool   `yaml:"autoenabled,omitempty" json:"autoEnabled,omitempty"`
}
