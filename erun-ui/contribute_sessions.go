package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// StartContributeSession spawns a shell in the operator's erun clone running the locally-built CLI.
func (a *App) StartContributeSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start contribute ERun session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindContributeERun, func(ctx context.Context) (uimodel.StartSessionResult, *terminal.Session, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, false)
	})
}

// StartContributeAISession is StartContributeSession with the env's AI assistant.
func (a *App) StartContributeAISession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start contribute AI session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindContributeAI, func(ctx context.Context) (uimodel.StartSessionResult, *terminal.Session, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, true)
	})
}

func (a *App) runContributeSession(ctx context.Context, selection uimodel.Selection, slot, cols, rows int, withAI bool) (uimodel.StartSessionResult, *terminal.Session, error) {
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

	kind := terminal.KindContributeERun
	if withAI {
		kind = terminal.KindContributeAI
	}

	if reused, managed, ok := a.reuseExistingContributeSession(key, slot, kind); ok {
		return reused, managed, nil
	}

	// Setup is the remote session's create-time program, so a reopen reconnects rather than re-running it.
	appSessionID := "contribute-erun"
	if withAI {
		appSessionID = "contribute-ai"
	}
	params := terminal.StartParams{
		Dir:        terminal.ResolveStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       terminal.WithAppSession(terminal.BuildOpenArgs(result.Tenant, result.Environment), appSessionID, withAI, true),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	var serial int
	var managed *terminal.Session
	a.terminal.WithLock(func(l *terminal.Locked) {
		serial = l.NextSerial()
		managed = &terminal.Session{
			PTY:                    session,
			Selection:              selection,
			Key:                    key,
			Serial:                 serial,
			Slot:                   slot,
			Kind:                   kind,
			AppSession:             appSessionID,
			BlocksIdleStop:         true,
			ClearIdleBlockOnOutput: true,
			Respawn: func() (terminal.PTY, error) {
				return a.deps.startTerminal(terminal.ReconnectParams(params))
			},
			StartedAt: time.Now(),
			LastCols:  cols,
			LastRows:  rows,
		}
		l.Register(managed)
		l.AddBusy(selection)
	})

	a.terminal.RecordActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.terminal.Stream(managed)
	go a.envops.StartWorkspaceSync(selection)
	go a.cloudContexts.StartRefresher(selection)

	tabName := "ERun contribute tab"
	if withAI {
		tabName = "AI contribute tab"
	}
	a.terminal.LogSpawnedCommandToLocal(selection, string(kind), terminal.FormatLocalCommandLog(terminal.FormatLaunchCommand(params), tabName))
	_ = ctx
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(kind),
	}, managed, nil
}

func (a *App) reuseExistingContributeSession(key string, slot int, kind terminal.Kind) (uimodel.StartSessionResult, *terminal.Session, bool) {
	var existing *terminal.Session
	a.terminal.WithLock(func(l *terminal.Locked) {
		existing = l.Session(key)
	})
	if existing == nil || existing.Closed || existing.PTY == nil {
		return uimodel.StartSessionResult{}, nil, false
	}
	existing.SignalReady(nil)
	return uimodel.StartSessionResult{
		SessionID: existing.Serial,
		Selection: existing.Selection,
		Slot:      slot,
		Kind:      string(kind),
	}, existing, true
}

func contributeSessionKey(selection uimodel.Selection, slot int, withAI bool) string {
	prefix := "contribute-erun"
	if withAI {
		prefix = "contribute-ai"
	}
	return prefix + "\x00" + uimodel.SelectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func (a *App) closeContributeSessionsForSelection(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	prefixes := []string{
		"contribute-erun\x00" + uimodel.SelectionKey(selection) + "\x00",
		"contribute-ai\x00" + uimodel.SelectionKey(selection) + "\x00",
	}
	a.terminal.WithLock(func(l *terminal.Locked) {
		for key, managed := range l.Sessions() {
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
			_ = managed.PTY.Close()
			l.Delete(key)
		}
	})
}
