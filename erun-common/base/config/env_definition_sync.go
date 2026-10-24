package config

// OrUnsetLocal renders the local side of a conflict, naming an empty value
// rather than leaving a blank the operator has to interpret.
func (c EnvDefinitionConflict) OrUnsetLocal() string { return valueOrUnset(c.Local) }

// OrUnsetPlatform renders the platform side of a conflict.
func (c EnvDefinitionConflict) OrUnsetPlatform() string { return valueOrUnset(c.Platform) }
