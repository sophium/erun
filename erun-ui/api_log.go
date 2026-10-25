package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
)

func loadAPILog(ctx context.Context, input tenantsvc.DashboardInput) (string, error) {
	kubernetesContext := strings.TrimSpace(input.KubernetesContext)
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	if kubernetesContext != "" && tenant != "" && environment != "" {
		return loadAPILogFromKubernetes(ctx, kubernetesContext, tenant, environment)
	}
	if mcpURL := strings.TrimSpace(input.MCPURL); mcpURL != "" {
		return loadAPILogFromMCP(ctx, mcpURL, input.MCPBearer)
	}
	return "", fmt.Errorf("tenant dashboard needs a Kubernetes context or MCP URL to load API logs")
}

func loadAPILogFromKubernetes(ctx context.Context, kubernetesContext, tenant, environment string) (string, error) {
	namespace := runtimetarget.KubernetesNamespaceName(tenant, environment)
	// A selector names the replica; Recreate rollouts briefly have two matching pods.
	return kubectlText(ctx, kubernetesContext,
		"--namespace", namespace,
		"logs",
		"-l", "app="+runtimeref.APIDeploymentName(tenant),
		"--prefix",
		"-c", "erun-backend-api",
		"--tail", "400",
	)
}

func kubectlText(ctx context.Context, kubernetesContext string, args ...string) (string, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext != "" {
		args = append([]string{"--context", kubernetesContext}, args...)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(output), "\n")
	if err != nil {
		if strings.TrimSpace(text) != "" {
			return "", fmt.Errorf("%w: %s", err, text)
		}
		return "", err
	}
	return text, nil
}
