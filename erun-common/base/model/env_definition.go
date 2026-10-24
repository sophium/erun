package model

// EnvConfigFieldClass classifies EnvConfig fields for definition transfer. The table
// is default-deny so a new field never travels until opted in; the root config.yaml
// (plaintext AdminToken) is permanently out of scope.
type EnvConfigFieldClass string

const (
	EnvFieldPortable EnvConfigFieldClass = "portable"
	// EnvFieldHostOwned fields describe this machine or a cluster-local object; never transferred.
	EnvFieldHostOwned EnvConfigFieldClass = "host-owned"
	// EnvFieldPlatformOwned fields mirror the environment row; a local difference refuses.
	EnvFieldPlatformOwned EnvConfigFieldClass = "platform-owned"
)
