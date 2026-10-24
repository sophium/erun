package shared

import (
	"github.com/sophium/erun/erun-common"
)

// The job tools are the host-side half of the job surface. An orchestrator
// driving an environment from outside starts long work here, then comes back for
// its state — instead of using `raw` to hand-roll detachment, a log redirect, a
// polling loop, a sentinel token, and a parse of this envelope to recover an
// exit code that a wrapping shell had already mangled.

// JobResult is the handle plus whatever is known about the job right now.
type JobResult struct {
	Tenant      string                    `json:"tenant"`
	Environment string                    `json:"environment"`
	Job         eruncommon.EnvironmentJob `json:"job"`
	Trace       []string                  `json:"trace,omitempty"`
	Executed    bool                      `json:"executed"`
}
