package main

import (
	"context"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// ListRemoteAppSessions reports the persistent desktop sessions running in the
// env's runtime pod so the frontend can rebuild tabs for custom terminals
// another ERun window created; the default tabs attach-or-create on their own.
// Fail-soft by design: unreachable or undeployed envs yield nil, never an
// error, so the open flow never stalls on detection.
//
// A socket with no live program behind it is deliberately excluded: rebuilding a
// tab for one would reattach to nothing and present a dead pane as a session.
func (a *App) ListRemoteAppSessions(selection uiSelection) []string {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), 5*time.Second)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection,
		eruncommon.RemoteAppSessionHeartbeatScript(selection.Tenant, selection.Environment))
	if err != nil {
		return nil
	}
	var ids []string
	for _, heartbeat := range eruncommon.ParseRemoteAppSessionHeartbeats(output) {
		if heartbeat.Running() {
			ids = append(ids, heartbeat.ID)
		}
	}
	return ids
}

// endRemoteAppSession permanently ends one persistent desktop session so
// detection will not rebuild its tab. Called only when the user explicitly
// closes a custom terminal tab; without it the session would outlive the close
// and resurrect on the next env open. Closing the env or quitting the app only
// detach — they never end a session. Fail-soft: a missing pod means the
// session is already gone.
func (a *App) endRemoteAppSession(selection uiSelection, sessionID string) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), 10*time.Second)
	defer cancel()
	_, _ = a.execInRuntimePod(ctx, selection,
		eruncommon.RemoteAppSessionEndScript(selection.Tenant, selection.Environment, sessionID))
}
