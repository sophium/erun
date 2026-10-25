package kubecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/projectpaths"
	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartfiles"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmargs"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"gopkg.in/yaml.v3"
)

func ResolveKubernetesDeployContext() (model.KubernetesDeployContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return model.KubernetesDeployContext{}, err
	}

	return KubernetesDeployContextAtDir(dir), nil
}

func ResolveCurrentKubernetesDeployContexts(findProjectRoot projectroot.ProjectFinderFunc, resolveDeployContext deploytarget.DeployContextResolverFunc, projectRootOverride string) ([]model.KubernetesDeployContext, error) {
	if resolveDeployContext == nil {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	deployContext, err := resolveDeployContext()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(deployContext.ChartPath) != "" && strings.TrimSpace(deployContext.ComponentName) != "" {
		deployContext.ComponentName = strings.TrimSpace(deployContext.ComponentName)
		deployContext.ChartPath = filepath.Clean(deployContext.ChartPath)
		if err := chartfiles.ValidateHelmChartPath(deployContext.ChartPath); err != nil {
			return nil, err
		}
		return []model.KubernetesDeployContext{deployContext}, nil
	}

	if deployContexts, err := dockerbuild.ResolveKubernetesDeployContextsAtDir(deployContext.Dir); err == nil {
		return deployContexts, nil
	}

	k8sDir, ok, err := dockerbuild.ResolveCurrentDevopsK8sDir(findProjectRoot, deployContext.Dir, projectRootOverride)
	if err != nil {
		return nil, err
	}
	if ok {
		return dockerbuild.ResolveKubernetesDeployContextsAtDir(k8sDir)
	}

	return nil, fmt.Errorf("helm chart not found in current directory")
}

func KubernetesDeployContextAtDir(dir string) model.KubernetesDeployContext {
	context := model.KubernetesDeployContext{Dir: dir}
	componentName := filepath.Base(dir)
	parentName := filepath.Base(filepath.Dir(dir))

	switch parentName {
	case "k8s":
		if dockerbuild.HasHelmChart(filepath.Join(dir, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = dir
		}
	case "docker":
		chartPath := filepath.Join(filepath.Dir(filepath.Dir(dir)), "k8s", componentName)
		if dockerbuild.HasHelmChart(filepath.Join(chartPath, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = chartPath
		}
	}

	return context
}

// TraceConfiguredDeployPaths also warns when the project config is gitignored,
// which voids the override.
func TraceConfiguredDeployPaths(ctx core.Context, repoPath string) {
	projectpaths.WarnIfProjectConfigGitIgnored(ctx, repoPath)
	paths, err := projectpaths.LoadProjectPaths(repoPath)
	if err != nil {
		return
	}
	if v := strings.TrimSpace(paths.K8s); v != "" {
		ctx.Trace("deploy: k8s chart root configured as " + v + " (.erun/config.yaml paths.k8s)")
	}
}

func CheckKubernetesDeployment(ctx core.Context, params model.KubernetesDeploymentCheckParams) (bool, error) {
	deployed, output, err := checkKubernetesDeploymentWithContext(ctx, params, params.KubernetesContext)
	if err == nil {
		return deployed, nil
	}
	// Inside an env's pod the configured context is the desktop's; the pod's
	// kubeconfig only has in-cluster, which current-context selects.
	if isKubernetesContextMissingMessage(output) && strings.TrimSpace(params.KubernetesContext) != "" {
		ctx.Trace(fmt.Sprintf("kubernetes deployment check: context %q not found in kubeconfig; retrying with current-context", params.KubernetesContext))
		fallback, _, fallbackErr := checkKubernetesDeploymentWithContext(ctx, params, "")
		if fallbackErr == nil {
			return fallback, nil
		}
		return false, fallbackErr
	}
	return false, err
}

// checkKubernetesDeploymentWithContext returns an error only when it cannot
// decide; absence is (false, output, nil).
func checkKubernetesDeploymentWithContext(ctx core.Context, params model.KubernetesDeploymentCheckParams, kubectlContext string) (bool, string, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(kubectlContext) != "" {
		args = append(args, "--context", kubectlContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "name")

	ctx.TraceCommand("", "kubectl", args...)
	rawOutput, err := core.Command("kubectl", args...).CombinedOutput()
	output := string(rawOutput)
	if err == nil {
		if !hasExpectedDeploymentSettings(params) {
			return true, output, nil
		}
		matchParams := params
		matchParams.KubernetesContext = kubectlContext
		deployed, matchErr := deploymentMatchesExpectedSettings(ctx, matchParams)
		return deployed, output, matchErr
	}

	if kube.KubernetesResourceNotFound(output) || strings.Contains(strings.ToLower(output), "no resources found") {
		return false, output, nil
	}

	detail := kubernetesDeploymentCheckFailureDetail(output, kubectlContext)
	sanitized := observe.SanitizeKubectlFailureOutput(output)
	// The raw output still reaches the per-env trace log.
	if trimmedRaw := strings.TrimSpace(output); trimmedRaw != "" {
		ctx.Logger.Debug("kubectl deployment check raw output: " + trimmedRaw)
	}
	if sanitized == "" {
		return false, output, fmt.Errorf("could not determine whether deployment %q is deployed (%s): %w", params.Name, detail, err)
	}
	return false, output, fmt.Errorf("could not determine whether deployment %q is deployed (%s): %s", params.Name, detail, sanitized)
}

// kubernetesDeploymentCheckFailureDetail names the context erun already knows,
// so truncating kubectl's output for display cannot lose the target.
func kubernetesDeploymentCheckFailureDetail(output, kubectlContext string) string {
	if isKubernetesContextMissingMessage(output) {
		return "the configured kubernetes context was not found in kubeconfig"
	}
	message := strings.ToLower(output)
	switch {
	case observe.KubernetesAPIServerUnreachableSignal(message):
		if kubectlContext = strings.TrimSpace(kubectlContext); kubectlContext != "" {
			return fmt.Sprintf("the kubernetes api server could not be reached (context %q)", kubectlContext)
		}
		return "the kubernetes api server could not be reached"
	case strings.Contains(message, "forbidden"),
		strings.Contains(message, "unauthorized"),
		strings.Contains(message, "must be logged in to the server"),
		strings.Contains(message, "must provide one of the following credentials"):
		return "the kubernetes cluster refused the request (credentials or permissions)"
	default:
		return "kubectl returned an error erun does not recognize"
	}
}

// KubernetesDeploymentAbsentMessageMarker lets callers holding only error text
// (e.g. the desktop) recognize a confirmed absence; an unresolved check never
// carries it.
const KubernetesDeploymentAbsentMessageMarker = "is not deployed (deployment"

// "no context exists with the name" is the older kubectl wording.
func isKubernetesContextMissingMessage(output string) bool {
	msg := strings.ToLower(output)
	if strings.Contains(msg, "does not exist") {
		return true
	}
	if strings.Contains(msg, "no context exists with the name") {
		return true
	}
	return false
}

func hasExpectedDeploymentSettings(params model.KubernetesDeploymentCheckParams) bool {
	return strings.TrimSpace(params.ExpectedRepoPath) != "" ||
		params.ExpectedSSHD != nil ||
		params.ExpectedMCPPort > 0 ||
		params.ExpectedSSHPort > 0 ||
		params.ExpectedRuntimePod != (model.RuntimePodResources{})
}

type deploymentEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func deploymentMatchesExpectedSettings(ctx core.Context, params model.KubernetesDeploymentCheckParams) (bool, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(params.KubernetesContext) != "" {
		args = append(args, "--context", params.KubernetesContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "json")

	ctx.TraceCommand("", "kubectl", args...)
	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		if sanitized := observe.SanitizeKubectlFailureOutput(string(output)); sanitized != "" {
			return false, fmt.Errorf("failed to inspect deployment %q: %w: %s", params.Name, err, sanitized)
		}
		return false, fmt.Errorf("failed to inspect deployment %q: %w", params.Name, err)
	}

	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name      string             `json:"name"`
						Env       []deploymentEnvVar `json:"env"`
						Resources struct {
							Limits model.RuntimePodResources `json:"limits"`
						} `json:"resources"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(output, &deployment); err != nil {
		return false, fmt.Errorf("failed to parse deployment %q: %w", params.Name, err)
	}

	matches := expectedDeploymentMatches(params)
	var runtimeLimits model.RuntimePodResources
	runtimeFound := false
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			matches.apply(params, env.Name, env.Value)
		}
		if strings.TrimSpace(container.Name) == params.Name {
			runtimeLimits = container.Resources.Limits
			runtimeFound = true
		}
	}
	if !runtimeFound {
		return false, nil
	}
	matches.runtimePod = matchesExpectedRuntimePod(runtimeLimits, params.ExpectedRuntimePod)
	return matches.ok(), nil
}

type deploymentExpectedMatches struct {
	repoPath   bool
	sshd       bool
	mcpPort    bool
	sshPort    bool
	runtimePod bool
}

// expectedDeploymentMatches starts unset expectations as true. ERUN_API_PORT is
// excluded: tenant charts may omit it, which would force a redeploy on every open.
func expectedDeploymentMatches(params model.KubernetesDeploymentCheckParams) deploymentExpectedMatches {
	return deploymentExpectedMatches{
		repoPath:   strings.TrimSpace(params.ExpectedRepoPath) == "",
		sshd:       params.ExpectedSSHD == nil,
		mcpPort:    params.ExpectedMCPPort <= 0,
		sshPort:    params.ExpectedSSHPort <= 0,
		runtimePod: params.ExpectedRuntimePod == (model.RuntimePodResources{}),
	}
}

func (m *deploymentExpectedMatches) apply(params model.KubernetesDeploymentCheckParams, name, value string) {
	switch strings.TrimSpace(name) {
	case "ERUN_REPO_PATH":
		m.repoPath = matchesExpectedRepoPath(value, params.ExpectedRepoPath)
	case "ERUN_SSHD_ENABLED":
		m.sshd = matchesExpectedBool(value, params.ExpectedSSHD)
	case "ERUN_MCP_PORT":
		m.mcpPort = matchesExpectedPort(value, params.ExpectedMCPPort)
	case "ERUN_SSHD_PORT":
		m.sshPort = matchesExpectedPort(value, params.ExpectedSSHPort)
	}
}

func (m deploymentExpectedMatches) ok() bool {
	return m.repoPath && m.sshd && m.mcpPort && m.sshPort && m.runtimePod
}

func matchesExpectedRepoPath(value, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return true
	}
	return filepath.Clean(strings.TrimSpace(value)) == filepath.Clean(strings.TrimSpace(expected))
}

func matchesExpectedBool(value string, expected *bool) bool {
	if expected == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value), helmargs.FormatHelmBool(*expected))
}

func matchesExpectedPort(value string, expected int) bool {
	if expected <= 0 {
		return true
	}
	return strings.TrimSpace(value) == fmt.Sprintf("%d", expected)
}

func matchesExpectedRuntimePod(value, expected model.RuntimePodResources) bool {
	if expected == (model.RuntimePodResources{}) {
		return true
	}
	value = runtimeresources.NormalizeRuntimePodResources(value)
	expected = runtimeresources.NormalizeRuntimePodResources(expected)
	valueCPU, valueCPUErr := runtimeresources.ParseKubernetesCPUToMilli(value.CPU)
	expectedCPU, expectedCPUErr := runtimeresources.ParseKubernetesCPUToMilli(expected.CPU)
	valueMemory, valueMemoryErr := runtimeresources.ParseKubernetesMemoryToMi(value.Memory)
	expectedMemory, expectedMemoryErr := runtimeresources.ParseKubernetesMemoryToMi(expected.Memory)
	return valueCPUErr == nil &&
		expectedCPUErr == nil &&
		valueMemoryErr == nil &&
		expectedMemoryErr == nil &&
		valueCPU == expectedCPU &&
		valueMemory == expectedMemory
}
