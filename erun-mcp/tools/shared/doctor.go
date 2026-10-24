package shared

import (
	"github.com/sophium/erun/erun-common/base/config"
)

// DoctorRootConfigReport pairs the inspection with the call's repair
// outcomes so an LLM agent can decide whether to retry with full
// per-alias init params or escalate to the user.
type DoctorRootConfigReport struct {
	Inspection                  config.RootConfigInspection `json:"inspection"`
	RestoredFromBackup          string                      `json:"restoredFromBackup,omitempty"`
	RestoredEnvConfigFromBackup string                      `json:"restoredEnvConfigFromBackup,omitempty"`
	RepairedAliases             []string                    `json:"repairedAliases,omitempty"`
	UnresolvedAliases           []string                    `json:"unresolvedAliases,omitempty"`
}
