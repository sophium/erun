package model

// SSHOrphanedAlias is an erun Host block in ~/.ssh/config that no configured environment claims.
type SSHOrphanedAlias struct {
	Alias string `json:"alias"`
	// Port is zero when the block declares none.
	Port int `json:"port,omitempty"`
	// ReachesTenant/ReachesEnvironment name the environment that now owns Port, so ssh to this
	// alias silently lands there. Empty means the block simply fails to resolve.
	ReachesTenant      string `json:"reachesTenant,omitempty"`
	ReachesEnvironment string `json:"reachesEnvironment,omitempty"`
}
