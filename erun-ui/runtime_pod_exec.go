package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// execInRuntimePod uses kubectl exec rather than the MCP edge so it works while the edge is down.
func (a *App) execInRuntimePod(ctx context.Context, selection uimodel.Selection, script string) (string, error) {
	if a.deps.execRuntimePod != nil {
		return a.deps.execRuntimePod(ctx, selection, script)
	}
	return "", fmt.Errorf("runtime pod exec is unavailable")
}

func execInRuntimePodViaKubectl(ctx context.Context, selection uimodel.Selection, store erunUIStore, script string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("configuration store is unavailable")
	}
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", err
	}
	kubernetesContext := strings.TrimSpace(envConfig.KubernetesContext)
	if kubernetesContext == "" {
		return "", fmt.Errorf("environment has no kubernetes context")
	}
	return kubectlText(ctx, kubernetesContext,
		"--namespace", config.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		"exec",
		"deployment/"+config.RuntimeReleaseName(selection.Tenant),
		"--",
		"/bin/sh", "-c", script,
	)
}
