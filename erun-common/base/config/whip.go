package config

// WhipConfigOverride is what a caller may persist in ~/.erun/config.yaml to
// change WhipConfig without a rebuild. Pointer fields so an unset field is
// distinguishable from an explicit zero (a StaleAfterSeconds of 0 or a
// disabled AutoEnabled are meaningful values, not "use the default").
type WhipConfigOverride struct {
	Message           *string `yaml:"message,omitempty" json:"message,omitempty"`
	StaleAfterSeconds *int    `yaml:"staleafterseconds,omitempty" json:"staleAfterSeconds,omitempty"`
	MaxNudges         *int    `yaml:"maxnudges,omitempty" json:"maxNudges,omitempty"`
	AutoEnabled       *bool   `yaml:"autoenabled,omitempty" json:"autoEnabled,omitempty"`
}
