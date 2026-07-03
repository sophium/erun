package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	sessionKindContributeERun sessionKind = "contribute-erun"
	sessionKindContributeAI   sessionKind = "contribute-ai"
)

// StartContributeSession spawns the contribute ERun tab: a shell in the
// operator's local erun clone where `erun` runs the locally-built CLI.
func (a *App) StartContributeSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "contribute-erun", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, false)
	})
}

// StartContributeAISession spawns the contribute AI tab: like the contribute
// ERun tab, but boots the env's AI assistant inside the clone.
func (a *App) StartContributeAISession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "contribute-ai", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, true)
	})
}

func (a *App) runContributeSession(ctx context.Context, selection uiSelection, slot, cols, rows int, withAI bool) (startSessionResult, *managedTerminal, error) {
	if !a.GetContributeMode(selection) {
		return startSessionResult{}, nil, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

	key := contributeSessionKey(selection, slot, withAI)
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, nil, err
	}

	kind := sessionKindContributeERun
	if withAI {
		kind = sessionKindContributeAI
	}

	if reused, managed, ok := a.reuseExistingContributeSession(key, slot, kind); ok {
		return reused, managed, nil
	}

	// Setup runs as the remote session's create-time program exactly once, so
	// reopening the tab reconnects to the live session instead of re-running
	// setup or spawning a parallel one.
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
		return startSessionResult{}, nil, err
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
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(params)
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
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(kind),
	}, managed, nil
}

func (a *App) reuseExistingContributeSession(key string, slot int, kind sessionKind) (startSessionResult, *managedTerminal, bool) {
	a.mu.Lock()
	existing := a.sessions[key]
	if existing == nil || existing.closed || existing.session == nil {
		a.mu.Unlock()
		return startSessionResult{}, nil, false
	}
	a.mu.Unlock()
	existing.signalReady(nil)
	return startSessionResult{
		SessionID: existing.serial,
		Selection: existing.selection,
		Slot:      slot,
		Kind:      string(kind),
	}, existing, true
}

func contributeSessionKey(selection uiSelection, slot int, withAI bool) string {
	prefix := "contribute-erun"
	if withAI {
		prefix = "contribute-ai"
	}
	return prefix + "\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

// closeContributeSessionsForSelection runs when the operator toggles
// contribute mode off for the env.
func (a *App) closeContributeSessionsForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
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
