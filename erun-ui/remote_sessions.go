package main

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// ListRemoteAppSessions reports the live persistent sessions in the env's pod.
// Fail-soft: unreachable envs yield nil so the open flow never stalls.
func (a *App) ListRemoteAppSessions(selection uiSelection) []string {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), 5*time.Second)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection,
		activity.RemoteAppSessionHeartbeatScript(selection.Tenant, selection.Environment))
	if err != nil {
		return nil
	}
	var ids []string
	for _, heartbeat := range activity.ParseRemoteAppSessionHeartbeats(output) {
		if heartbeat.Running() {
			ids = append(ids, heartbeat.ID)
		}
	}
	return ids
}

// endRemoteAppSession ends a session when the user closes its tab, so it is not
// rebuilt on the next open. Fail-soft.
func (a *App) endRemoteAppSession(selection uiSelection, sessionID string) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), 10*time.Second)
	defer cancel()
	_, _ = a.execInRuntimePod(ctx, selection,
		open.RemoteAppSessionEndScript(selection.Tenant, selection.Environment, sessionID))
}
