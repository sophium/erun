package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const (
	sessionKindContributeERun sessionKind = "contribute-erun"
	sessionKindContributeAI   sessionKind = "contribute-ai"
)

// StartContributeSession spawns a shell in the operator's erun clone running the locally-built CLI.
func (a *App) StartContributeSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start contribute ERun session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindContributeERun, func(ctx context.Context) (uimodel.StartSessionResult, *managedTerminal, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, false)
	})
}

// StartContributeAISession is StartContributeSession with the env's AI assistant.
func (a *App) StartContributeAISession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start contribute AI session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindContributeAI, func(ctx context.Context) (uimodel.StartSessionResult, *managedTerminal, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, true)
	})
}

func (a *App) runContributeSession(ctx context.Context, selection uimodel.Selection, slot, cols, rows int, withAI bool) (uimodel.StartSessionResult, *managedTerminal, error) {
	if !a.GetContributeMode(selection) {
		return uimodel.StartSessionResult{}, nil, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

	key := contributeSessionKey(selection, slot, withAI)
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	kind := sessionKindContributeERun
	if withAI {
		kind = sessionKindContributeAI
	}

	if reused, managed, ok := a.reuseExistingContributeSession(key, slot, kind); ok {
		return reused, managed, nil
	}

	// Setup is the remote session's create-time program, so a reopen reconnects rather than re-running it.
	appSessionID := "contribute-erun"
	if withAI {
		appSessionID = "contribute-ai"
	}
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       withAppSession(buildOpenArgs(result.Tenant, result.Environment), appSessionID, withAI, true),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:                session,
		selection:              selection,
		key:                    key,
		serial:                 serial,
		slot:                   slot,
		kind:                   kind,
		appSession:             appSessionID,
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(reconnectSessionParams(params))
		},
		startedAt: time.Now(),
		lastCols:  cols,
		lastRows:  rows,
	}
	a.sessions[key] = managed
	a.busyEnvs[selectionKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.streamSession(managed)
	go a.startWorkspaceSyncForSelection(selection)
	go a.startCloudCredentialsRefresherForSelection(selection)

	tabName := "ERun contribute tab"
	if withAI {
		tabName = "AI contribute tab"
	}
	a.logSpawnedCommandToLocal(selection, string(kind), formatLocalCommandLog(formatLaunchCommand(params), tabName))
	_ = ctx
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(kind),
	}, managed, nil
}

func (a *App) reuseExistingContributeSession(key string, slot int, kind sessionKind) (uimodel.StartSessionResult, *managedTerminal, bool) {
	a.mu.Lock()
	existing := a.sessions[key]
	if existing == nil || existing.closed || existing.session == nil {
		a.mu.Unlock()
		return uimodel.StartSessionResult{}, nil, false
	}
	a.mu.Unlock()
	existing.signalReady(nil)
	return uimodel.StartSessionResult{
		SessionID: existing.serial,
		Selection: existing.selection,
		Slot:      slot,
		Kind:      string(kind),
	}, existing, true
}

func contributeSessionKey(selection uimodel.Selection, slot int, withAI bool) string {
	prefix := "contribute-erun"
	if withAI {
		prefix = "contribute-ai"
	}
	return prefix + "\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func (a *App) closeContributeSessionsForSelection(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	prefixes := []string{
		"contribute-erun\x00" + selectionKey(selection) + "\x00",
		"contribute-ai\x00" + selectionKey(selection) + "\x00",
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, managed := range a.sessions {
		if managed == nil {
			continue
		}
		matches := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		_ = managed.session.Close()
		delete(a.sessions, key)
	}
}
