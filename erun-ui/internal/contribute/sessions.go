package contribute

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// RunSession spawns a shell in the operator's erun clone running the
// locally-built CLI, with the env's AI assistant when withAI; a live tab under
// the same key is reused instead.
func (s *Service) RunSession(selection uimodel.Selection, slot, cols, rows int, withAI bool) (uimodel.StartSessionResult, Readiness, error) {
	if !s.Mode(selection) {
		return uimodel.StartSessionResult{}, nil, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

	key := sessionKey(selection, slot, withAI)
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	kind := KindERun
	if withAI {
		kind = KindAI
	}

	if reused, ok := s.d.Sessions.Reuse(key); ok {
		return uimodel.StartSessionResult{
			SessionID: reused.Serial,
			Selection: reused.Selection,
			Slot:      slot,
			Kind:      kind,
		}, reused.Ready, nil
	}

	tabName := "ERun contribute tab"
	if withAI {
		tabName = "AI contribute tab"
	}
	// Setup is the remote session's create-time program, so a reopen reconnects rather than re-running it.
	serial, ready, err := s.d.Sessions.Spawn(Launch{
		Selection:   selection,
		Key:         key,
		Slot:        slot,
		Kind:        kind,
		AppSession:  kind,
		WithAI:      withAI,
		Tenant:      result.Tenant,
		Environment: result.Environment,
		RepoPath:    result.RepoPath,
		Cols:        cols,
		Rows:        rows,
		TabName:     tabName,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      kind,
	}, ready, nil
}

func sessionKeyPrefix(kind string, selection uimodel.Selection) string {
	return kind + "\x00" + uimodel.SelectionKey(selection) + "\x00"
}

func sessionKey(selection uimodel.Selection, slot int, withAI bool) string {
	kind := KindERun
	if withAI {
		kind = KindAI
	}
	return sessionKeyPrefix(kind, selection) + fmt.Sprintf("%d", slot)
}

func (s *Service) closeSessionsForSelection(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	s.d.Sessions.CloseWithKeyPrefix(sessionKeyPrefix(KindERun, selection), sessionKeyPrefix(KindAI, selection))
}
