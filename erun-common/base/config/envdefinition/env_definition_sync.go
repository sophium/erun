package envdefinition

import (
	"github.com/sophium/erun/erun-common/base/config"
)

// OrUnsetLocal renders the local side of a conflict, naming an empty value.
func (c EnvDefinitionConflict) OrUnsetLocal() string { return config.ValueOrUnset(c.Local) }

func (c EnvDefinitionConflict) OrUnsetPlatform() string { return config.ValueOrUnset(c.Platform) }
