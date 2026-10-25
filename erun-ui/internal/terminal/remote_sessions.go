package terminal

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ListRemoteAppSessions reports the live persistent sessions in the env's pod.
// Fail-soft: unreachable envs yield nil so the open flow never stalls.
func (s *Service) ListRemoteAppSessions(selection uimodel.Selection) []string {
	selection = uimodel.NormalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.d.Context(), 5*time.Second)
	defer cancel()
	output, err := s.d.ExecRuntimePod(ctx, selection,
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
func (s *Service) endRemoteAppSession(selection uimodel.Selection, sessionID string) {
	selection = uimodel.NormalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(s.d.Context(), 10*time.Second)
	defer cancel()
	_, _ = s.d.ExecRuntimePod(ctx, selection,
		appsession.RemoteAppSessionEndScript(selection.Tenant, selection.Environment, sessionID))
}
