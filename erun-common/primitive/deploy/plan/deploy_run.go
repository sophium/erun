package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
)

// LoadProjectK8sPlanForDeploy reads the plan once: all specs share a repo and
// environment. A malformed config errors rather than shipping a wrong plan.
func LoadProjectK8sPlanForDeploy(executions []seam.DeploySpec) (configpkg.ProjectK8sConfig, error) {
	for _, execution := range executions {
		plan, err := LoadProjectK8sPlanForRepo(execution.Target.RepoPath, execution.Target.Environment)
		if err != nil {
			return configpkg.ProjectK8sConfig{}, err
		}
		if !plan.IsZero() {
			return plan, nil
		}
	}
	return configpkg.ProjectK8sConfig{}, nil
}

func LoadProjectK8sPlanForRepo(repoPath, environment string) (configpkg.ProjectK8sConfig, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return configpkg.ProjectK8sConfig{}, nil
	}
	config, _, err := configpkg.LoadProjectConfig(repoPath)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return configpkg.ProjectK8sConfig{}, nil
		}
		return configpkg.ProjectK8sConfig{}, err
	}
	// Validate the platform block here so it fails the plan up front.
	if err := config.Platform.Validate(); err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	return config.K8sForEnvironment(environment), nil
}

// ResolvePublishedDeployPlan falls back to the cwd project when the configured
// repo path (the pod's own) is absent on this machine, tracing each branch.
func ResolvePublishedDeployPlan(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, resolvedTarget configpkg.OpenResult) (configpkg.ProjectK8sConfig, error) {
	configuredPath := strings.TrimSpace(resolvedTarget.RepoPath)
	plan, err := LoadProjectK8sPlanForRepo(configuredPath, resolvedTarget.Environment)
	if err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	if !plan.IsZero() {
		return plan, nil
	}
	if configuredPath == "" || projectDirReachable(configuredPath) {
		return configpkg.ProjectK8sConfig{}, nil
	}

	unreachableNotice := "deploy: configured repo path " + configuredPath + " is not present on this machine; "
	fallbackPath := resolveDeployPlanFallbackPath(findProjectRoot, configuredPath)
	if fallbackPath == "" {
		ctx.Trace(unreachableNotice + "no k8s.deployments plan could be read")
		return configpkg.ProjectK8sConfig{}, nil
	}
	fallbackPlan, err := LoadProjectK8sPlanForRepo(fallbackPath, resolvedTarget.Environment)
	if err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	if fallbackPlan.IsZero() {
		ctx.Trace(unreachableNotice + "no k8s.deployments plan could be read")
		return configpkg.ProjectK8sConfig{}, nil
	}
	ctx.Trace(unreachableNotice + "using the k8s.deployments plan from " + fallbackPath + " instead")
	return fallbackPlan, nil
}

func resolveDeployPlanFallbackPath(findProjectRoot seam.ProjectFinderFunc, configuredPath string) string {
	_, fallbackPath, err := findProjectRoot()
	fallbackPath = strings.TrimSpace(fallbackPath)
	if err != nil || fallbackPath == "" || filepath.Clean(fallbackPath) == filepath.Clean(configuredPath) {
		return ""
	}
	return fallbackPath
}

func projectDirReachable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
