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

// StartContributeSession spawns the contribute ERun tab: an `erun open`
// PTY against the env, then pipes a prelude that prepends the contribute
// shim directory to PATH, aliases `erun` to the local build script for
// the interactive shell, and cds into ~/git/erun. Subsequent `erun`
// invocations (whether typed at the prompt or spawned as child
// processes) resolve to the locally-built CLI.
func (a *App) StartContributeSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "contribute-erun", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runContributeSession(ctx, selection, slot, cols, rows, false)
	})
}

// StartContributeAISession spawns the contribute AI tab: same as
// StartContributeSession but additionally pipes the env's configured AI
// tool command after the contribute prelude so the AI assistant boots
// inside the contribute clone.
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

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		existing.signalReady(nil)
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
			Kind:      string(kind),
		}, existing, nil
	}
	a.mu.Unlock()

	prelude := buildContributePreludeCommand(withAI, result.EnvConfig.AITool)
	params := startTerminalSessionParams{
		Dir:          resolveTerminalStartDir(result.RepoPath),
		Executable:   a.deps.resolveCLIPath(),
		Args:         buildOpenArgs(result.Tenant, result.Environment, selection.Debug),
		Env:          []string{appSessionEnvVar + "=1"},
		Cols:         cols,
		Rows:         rows,
		InitialInput: []byte(prelude),
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
	}
	a.sessions[key] = managed
	a.busyEnvs[environmentBusyKey(selection)]++
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

// buildContributePreludeCommand returns the keystrokes piped into the
// contribute tab's shell so the contribute clone becomes the working
// directory and the shim becomes the resolved `erun`. The AI variant
// additionally invokes the env's configured AI tool.
//
// ERUN_SKIP_LINT=1 is set in the contribute shell because the user
// is iterating — they want fast incremental rebuilds, not a full
// typecheck+lint+format gate on every `erun app`. The host-side
// `erun app` workflow leaves the gates on (no contribute prelude
// runs there), and CI still enforces them.
func buildContributePreludeCommand(withAI bool, aiTool string) string {
	parts := []string{
		`export PATH="$HOME/.erun/contribute/bin:$PATH"`,
		`export ERUN_SKIP_LINT=1`,
		`cd "$HOME/git/erun"`,
		`clear`,
	}
	prelude := strings.Join(parts, " && ") + "\n"
	if withAI {
		tool := resolveAIToolCommand(aiTool)
		prelude += tool + "\n"
	}
	return prelude
}

func contributeSessionKey(selection uiSelection, slot int, withAI bool) string {
	prefix := "contribute-erun"
	if withAI {
		prefix = "contribute-ai"
	}
	return prefix + "\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

// closeContributeSessionsForSelection terminates any active contribute
// tabs for the env. Called when the user toggles contribute mode off.
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
