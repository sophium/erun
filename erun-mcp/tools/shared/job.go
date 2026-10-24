package shared

import (
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// JobResult is the job handle plus its current known state.
type JobResult struct {
	Tenant      string                `json:"tenant"`
	Environment string                `json:"environment"`
	Job         shared.EnvironmentJob `json:"job"`
	Trace       []string              `json:"trace,omitempty"`
	Executed    bool                  `json:"executed"`
}
