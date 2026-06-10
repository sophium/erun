package main

import (
	"context"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// ListRemoteAppSessions reports the persistent desktop sessions currently
// running in the env's runtime pod, by session id (`open-0`, `ai`,
// `contribute-erun`, …). The frontend calls it when an env opens so it can
// rebuild tabs for sessions another ERun window created (custom terminals);
// the default tabs attach-or-create on their own. Fail-soft by design: an
// env without a kubernetes context, an unreachable cluster, or a pod that is
// not deployed yields nil rather than an error — the open flow must never
// stall on detection.
func (a *App) ListRemoteAppSessions(selection uiSelection) []string {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return nil
	}
	kubernetesContext := strings.TrimSpace(envConfig.KubernetesContext)
	if kubernetesContext == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := kubectlText(ctx, kubernetesContext,
		"--namespace", eruncommon.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		"exec",
		"deployment/"+eruncommon.RuntimeReleaseName(selection.Tenant),
		"--",
		"/bin/sh", "-c", "ls "+eruncommon.RemoteAppSessionSocketDir+" 2>/dev/null || true",
	)
	if err != nil {
		return nil
	}
	return eruncommon.ParseRemoteAppSessionIDs(selection.Tenant, selection.Environment, output)
}

// endRemoteAppSession permanently ends one persistent desktop session in the
// env's runtime pod: the dtach master is killed (its shell/claude follows via
// SIGHUP) and the socket is removed, so detection will not rebuild the tab.
// Called when the user explicitly closes a custom terminal tab — the X is
// that tab's only removal affordance, and without this the session would
// outlive the close and resurrect on the next env open. Closing the env or
// quitting the app never calls this; those only detach. Fail-soft: a missing
// pod means the session is already gone.
func (a *App) endRemoteAppSession(selection uiSelection, sessionID string) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return
	}
	kubernetesContext := strings.TrimSpace(envConfig.KubernetesContext)
	if kubernetesContext == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = kubectlText(ctx, kubernetesContext,
		"--namespace", eruncommon.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		"exec",
		"deployment/"+eruncommon.RuntimeReleaseName(selection.Tenant),
		"--",
		"/bin/sh", "-c", eruncommon.RemoteAppSessionEndScript(selection.Tenant, selection.Environment, sessionID),
	)
}
