package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

func loadAPILog(ctx context.Context, input uiTenantDashboardInput) (string, error) {
	kubernetesContext := strings.TrimSpace(input.KubernetesContext)
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	if kubernetesContext != "" && tenant != "" && environment != "" {
		return loadAPILogFromKubernetes(ctx, kubernetesContext, tenant, environment)
	}
	if mcpURL := strings.TrimSpace(input.MCPURL); mcpURL != "" {
		return loadAPILogFromMCP(ctx, mcpURL, input.mcpBearer)
	}
	return "", fmt.Errorf("tenant dashboard needs a Kubernetes context or MCP URL to load API logs")
}

func loadAPILogFromKubernetes(ctx context.Context, kubernetesContext, tenant, environment string) (string, error) {
	namespace := eruncommon.KubernetesNamespaceName(tenant, environment)
	// A selector (rather than deployment/<name>) names which replica the log
	// came from instead of quietly picking one; the API Deployment's
	// Recreate strategy means a rollout transiently has two pods matching.
	return kubectlText(ctx, kubernetesContext,
		"--namespace", namespace,
		"logs",
		"-l", "app="+eruncommon.APIDeploymentName(tenant),
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
	eruncommon.HideConsoleWindow(cmd)
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
