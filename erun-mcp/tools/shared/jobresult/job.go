package jobresult

import (
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// JobResult is the job handle plus its current known state.
type JobResult struct {
	Tenant      string                  `json:"tenant"`
	Environment string                  `json:"environment"`
	Job         jobstate.EnvironmentJob `json:"job"`
	Trace       []string                `json:"trace,omitempty"`
	Executed    bool                    `json:"executed"`
}
