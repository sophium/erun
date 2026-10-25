package main

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// emitAppStatus narrates a busy indicator; outcomes go through notifications instead.
func (a *App) emitAppStatus(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppStatusEvent, uimodel.AppStatusPayload{Message: message, Busy: true})
}

// emitAppNotification pushes a toast with no env/orchestrator identity.
func (a *App) emitAppNotification(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
}

const notificationSourceDeployFailed = "deploy-failed"

// notificationSourceOrchestratorEdgeUnreachable is posted per linked env, carrying that env's deploy action.
const notificationSourceOrchestratorEdgeUnreachable = "orchestrator-edge-unreachable"

const notificationSourceMCPUnreachable = "mcp-unreachable"

const notificationSourceCredentialRefreshFailed = "credential-refresh-failed"

const notificationActionDeploy = uimodel.NotificationActionDeploy

const notificationActionRestartOrchestrator = "restart-orchestrator"

// notificationActionInstallAndRestartOrchestrator also links the CLI install docs.
const notificationActionInstallAndRestartOrchestrator = "install-and-restart-orchestrator"

func (a *App) notifyCredentialRefreshFailure(selection uimodel.Selection, message string) {
	a.emitEnvNotification("error", selection.Tenant, selection.Environment, notificationSourceCredentialRefreshFailed, message, "")
}

// emitEnvNotification posts an env-scoped notification that emitClearEnvNotification
// can dismiss by (source, tenant, environment).
func (a *App) emitEnvNotification(kind, tenant, environment, source, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:        kind,
		Message:     message,
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
		Action:      action,
	})
}

// emitOrchestratorNotification tags the orchestrator so the frontend can act on it.
func (a *App) emitOrchestratorNotification(kind, orchestratorID, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:           kind,
		Message:        message,
		OrchestratorID: orchestratorID,
		Action:         action,
	})
}

// emitClearEnvNotification dismisses an emitEnvNotification matching source, tenant, and environment.
func (a *App) emitClearEnvNotification(tenant, environment, source string) {
	a.emit(uimodel.AppNotificationClearEvent, uimodel.AppNotificationClearPayload{
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}
