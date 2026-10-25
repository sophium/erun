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

const notificationSourceCredentialRefreshFailed = "credential-refresh-failed"

func (a *App) notifyCredentialRefreshFailure(selection uimodel.Selection, message string) {
	a.emitEnvNotification("error", selection.Tenant, selection.Environment, notificationSourceCredentialRefreshFailed, message, "")
}

// emitEnvNotification posts an env-scoped notification that a clear can dismiss
// by (source, tenant, environment).
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
