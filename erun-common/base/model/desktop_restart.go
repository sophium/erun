package model

// DesktopRestartStatus keeps success, refusal, and failure distinct.
type DesktopRestartStatus string

const (
	DesktopRestartWouldRestart DesktopRestartStatus = "would-restart"
	DesktopRestartRestarted    DesktopRestartStatus = "restarted"
	// DesktopRestartRefused means no live target was verified, so nothing was attempted.
	DesktopRestartRefused DesktopRestartStatus = "refused"
	// DesktopRestartFailed means the target was reached but reported the restart failed.
	DesktopRestartFailed DesktopRestartStatus = "failed"
)

type DesktopRestartOutcome struct {
	Status      DesktopRestartStatus `json:"status"`
	Reason      string               `json:"reason,omitempty"`
	PID         int                  `json:"pid,omitempty"`
	ControlPort int                  `json:"controlPort,omitempty"`
	// Preview is the dry run's answer: orchestrators the restart would reopen.
	Preview []DesktopRestartReopen `json:"preview,omitempty"`
	// PreviewUnavailable keeps an unreadable plan from reading as "nothing would be refused".
	PreviewUnavailable string `json:"previewUnavailable,omitempty"`
}

// DesktopRestartReopen is one orchestrator a restart reopens; its background agent keeps running.
type DesktopRestartReopen struct {
	OrchestratorID string `json:"orchestratorId"`
	// Notice is why the next launch would refuse to reopen this orchestrator.
	Notice string `json:"notice,omitempty"`
}
