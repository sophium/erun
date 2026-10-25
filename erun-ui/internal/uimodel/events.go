package uimodel

// Emitter publishes a frontend event. Services receive the App's emitter rather
// than calling the Wails runtime, so the headless server's emitter and test
// recorders reach every emit.
type Emitter func(name string, args ...any)

// Event names are the frontend's subscription contract.
const (
	TerminalOutputEvent           = "terminal-output"
	TerminalExitEvent             = "terminal-exit"
	AppStatusEvent                = "app-status"
	AppNotificationEvent          = "app-notification"
	AppNotificationClearEvent     = "app-notification-clear"
	MCPReconnectLineEvent         = "mcp-reconnect-line"
	EnvironmentInitializedEvent   = "environment-initialized"
	EnvironmentInitFailedEvent    = "environment-init-failed"
	EnvironmentDeployedEvent      = "environment-deployed"
	EnvironmentsChangedEvent      = "environments-changed"
	HostedDefinitionUploadedEvent = "hosted-definition-uploaded"
	DoctorCompletedEvent          = "doctor-completed"
	SSHDInitCompletedEvent        = "sshd-init-completed"
	AIActivityEvent               = "ai-activity"
	OrchestratorShellEvent        = "orchestrator-shell-activity"
	EnvStatusEvent                = "env-status"
	EnvActivityEvent              = "env-activity"
	EnvUsageEvent                 = "env-usage"
	EnvNodeEvent                  = "env-node"
	AppCloseGateEvent             = "app-close-gate"
)

// Env-status values; "" clears. Stopped context and stopped runtime differ in
// recovery.
const (
	EnvStatusStopped        = "stopped"
	EnvStatusRuntimeStopped = "runtime-stopped"
	EnvStatusFailed         = "failed"
)

// TerminalOutputPayload carries a base64 chunk of a session's PTY output.
type TerminalOutputPayload struct {
	SessionID int    `json:"sessionId"`
	Data      string `json:"data"`
}

// EnvStatusPayload: tab presence alone does not mean running.
type EnvStatusPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
}

type AppStatusPayload struct {
	Message string `json:"message"`
	Busy    bool   `json:"busy"`
}

// AppNotificationPayload: success/info auto-dismiss; warning/error persist.
// debug is never emitted by this backend.
type AppNotificationPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// Tenant/Environment/Source let a later state change dismiss this notification.
	Tenant      string `json:"tenant,omitempty"`
	Environment string `json:"environment,omitempty"`
	Source      string `json:"source,omitempty"`
	// OrchestratorID lets the frontend act on that orchestrator.
	OrchestratorID string `json:"orchestratorId,omitempty"`
	// Action names a control that performs the remedy; empty means none.
	Action string `json:"action,omitempty"`
}

// AppNotificationClearPayload clears only an exact source/tenant/environment match.
type AppNotificationClearPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Source      string `json:"source"`
}

// NotificationActionDeploy opens the tagged env's deploy dialog; "" means no action.
const NotificationActionDeploy = "deploy"
