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
