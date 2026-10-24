package model

import (
	"fmt"
)

// EnvDefinitionRefusalError is a difference a pull cannot ask about: a
// platform-owned field differing locally, or a host-owned field on the platform.
type EnvDefinitionRefusalError struct {
	Field  string              `json:"field"`
	Class  EnvConfigFieldClass `json:"class"`
	Reason string              `json:"reason"`
}

func (r EnvDefinitionRefusalError) Error() string {
	return fmt.Sprintf("%s is %s and cannot be transferred: %s", r.Field, r.Class, r.Reason)
}
