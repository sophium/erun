package config

// OrUnsetLocal renders the local side of a conflict, naming an empty value.
func (c EnvDefinitionConflict) OrUnsetLocal() string { return valueOrUnset(c.Local) }

func (c EnvDefinitionConflict) OrUnsetPlatform() string { return valueOrUnset(c.Platform) }
