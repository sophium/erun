package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
)

func ResolveKubernetesDeployContext() (shared.KubernetesDeployContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return shared.KubernetesDeployContext{}, err
	}

	return KubernetesDeployContextAtDir(dir), nil
}

func ResolveCurrentKubernetesDeployContexts(findProjectRoot shared.ProjectFinderFunc, resolveDeployContext DeployContextResolverFunc, projectRootOverride string) ([]shared.KubernetesDeployContext, error) {
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
		if err := ValidateHelmChartPath(deployContext.ChartPath); err != nil {
			return nil, err
		}
		return []shared.KubernetesDeployContext{deployContext}, nil
	}

	if deployContexts, err := shared.ResolveKubernetesDeployContextsAtDir(deployContext.Dir); err == nil {
		return deployContexts, nil
	}

	k8sDir, ok, err := shared.ResolveCurrentDevopsK8sDir(findProjectRoot, deployContext.Dir, projectRootOverride)
	if err != nil {
		return nil, err
	}
	if ok {
		return shared.ResolveKubernetesDeployContextsAtDir(k8sDir)
	}

	return nil, fmt.Errorf("helm chart not found in current directory")
}

func KubernetesDeployContextAtDir(dir string) shared.KubernetesDeployContext {
	context := shared.KubernetesDeployContext{Dir: dir}
	componentName := filepath.Base(dir)
	parentName := filepath.Base(filepath.Dir(dir))

	switch parentName {
	case "k8s":
		if shared.HasHelmChart(filepath.Join(dir, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = dir
		}
	case "docker":
		chartPath := filepath.Join(filepath.Dir(filepath.Dir(dir)), "k8s", componentName)
		if shared.HasHelmChart(filepath.Join(chartPath, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = chartPath
		}
	}

	return context
}

// traceConfiguredDeployPaths surfaces a configured paths.k8s override as a
// dry-run decision line so the deploy plan shows the chart dir was resolved from
// config rather than the -devops convention. It also warns when the project
// config itself is gitignored, since that silently voids the very override it
// just traced.
func traceConfiguredDeployPaths(ctx core.Context, repoPath string) {
	configpkg.WarnIfProjectConfigGitIgnored(ctx, repoPath)
	paths, err := configpkg.LoadProjectPaths(repoPath)
	if err != nil {
		return
	}
	if v := strings.TrimSpace(paths.K8s); v != "" {
		ctx.Trace("deploy: k8s chart root configured as " + v + " (.erun/config.yaml paths.k8s)")
	}
}

func CheckKubernetesDeployment(ctx core.Context, params KubernetesDeploymentCheckParams) (bool, error) {
	deployed, output, err := checkKubernetesDeploymentWithContext(ctx, params, params.KubernetesContext)
	if err == nil {
		return deployed, nil
	}
	// `kubectl --context <name>` returns a "context does not exist"
	// error when the requested context is missing from the local
	// kubeconfig. The most common case is the contribute clone
	// running inside an env's pod: the env config it reads has the
	// *outer* desktop's context name (e.g.
	// `erun-001-…-eu-west-2`), but the pod's kubeconfig only has
	// `in-cluster`. Falling back to a context-less call uses
	// kubectl's current-context, which on the pod is the in-cluster
	// service-account context — exactly what we want. On a desktop
	// where the configured context name *is* valid, we never enter
	// this branch.
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

// checkKubernetesDeploymentWithContext runs the underlying kubectl
// get + (optional) match check for the supplied context. Returns
// the captured combined output alongside the error so the caller can
// inspect kubectl's stderr (which carries the "context does not
// exist" string that drives the retry-with-current-context fallback
// in CheckKubernetesDeployment).
//
// A non-nil error here is never evidence the deployment is absent — that
// case returns (false, output, nil) below. It means the check could not
// resolve a definite answer, so the message names the recognized cause
// (missing context, unreachable API server, credentials/permissions
// refused) or, failing that, says plainly that the error is unrecognized —
// never silently reads as "not deployed". The message carries kubectl's own
// output instead of just the process's exit status, so a caller sees the
// actual cause rather than two layers of the same content-free "exit status
// 1".
func checkKubernetesDeploymentWithContext(ctx core.Context, params KubernetesDeploymentCheckParams, kubectlContext string) (bool, string, error) {
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
	sanitized := sanitizeKubectlFailureOutput(output)
	// The sanitized sentence is what reaches the operator; kubectl's raw
	// combined output still goes to the per-env trace log (ActivateEnvTrace
	// tees it unconditionally, so this reaches disk regardless of terminal
	// verbosity) so the full diagnostic is never actually lost, only kept out
	// of a surface too narrow to show it.
	if trimmedRaw := strings.TrimSpace(output); trimmedRaw != "" {
		ctx.Logger.Debug("kubectl deployment check raw output: " + trimmedRaw)
	}
	if sanitized == "" {
		return false, output, fmt.Errorf("could not determine whether deployment %q is deployed (%s): %w", params.Name, detail, err)
	}
	return false, output, fmt.Errorf("could not determine whether deployment %q is deployed (%s): %s", params.Name, detail, sanitized)
}

// kubernetesAPIServerUnreachableSignal reports whether kubectl/helm's raw
// output signals that the Kubernetes API server itself could not be
// reached, as opposed to any other failure (RBAC, a missing resource, a
// malformed chart). kubernetesDeploymentCheckFailureDetail and the doctor
// deploy diagnosis (doctor_deploy.go) both classify off this same signal so
// "cluster unreachable" cannot drift between the two call sites.
func kubernetesAPIServerUnreachableSignal(output string) bool {
	message := strings.ToLower(output)
	return strings.Contains(message, "unable to connect to the server") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "i/o timeout") ||
		strings.Contains(message, "no configuration has been provided")
}

// kubernetesDeploymentCheckFailureDetail names why a kubectl deployment
// presence check failed to resolve a definite answer, distinguishing causes
// where erun could not ask the cluster at all (no context, an unreachable
// API server, credentials or permissions refused) from an error it does not
// recognize. Neither is evidence the deployment is absent. Reuses
// isKubernetesContextMissingMessage for the context-missing case it already
// covers instead of a second matcher for the same signal. Names the
// kubectl context for the unreachable-API-server case (erun#1766): erun
// already knows it from its own configuration, so the message can identify
// the target cluster without depending on a substring of kubectl's own
// output surviving whatever later truncates the message for display.
func kubernetesDeploymentCheckFailureDetail(output, kubectlContext string) string {
	if isKubernetesContextMissingMessage(output) {
		return "the configured kubernetes context was not found in kubeconfig"
	}
	message := strings.ToLower(output)
	switch {
	case kubernetesAPIServerUnreachableSignal(message):
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

// klogFramePattern matches the severity+timestamp+goroutine-id+source-location
// prefix klog stamps on every line it emits (e.g. `E0831 13:46:43.793908
// 10289 memcache.go:265]`). None of it is operator-relevant: the
// severity/timestamp duplicate context the caller's own message already
// carries, and the goroutine id and Go source file are internal to
// kubectl/client-go.
var klogFramePattern = regexp.MustCompile(`^[EWIF]\d{4} \d{2}:\d{2}:\d{2}\.\d+\s+\d+ \S+\.go:\d+\]\s*`)

// klogUnhandledErrorPattern extracts the inner err="..." sentence klog's
// "Unhandled Error" wrapper carries -- the one piece of a klog line an
// operator can act on. The capture is greedy so it consumes through the
// line's real closing quote rather than stopping at an escaped `\"` inside
// the nested message.
var klogUnhandledErrorPattern = regexp.MustCompile(`^"Unhandled Error"\s+err="(.*)"$`)

// sanitizeKubectlFailureOutput reduces kubectl's raw combined output to the
// one sentence an operator can act on, rather than pasting a klog line
// verbatim (erun#1766: a toast rendered that line at a fixed width, so the
// goroutine id and source location survived while the API server address --
// the one piece of the message that actually named a cause -- was what got
// cut off). It scans from the last non-blank line backward, strips klog's
// frame if present, and unwraps an "Unhandled Error" carrier down to its
// err= text; a line with neither shape is returned frame-stripped as-is, so
// an unrecognized failure still reaches the operator rather than
// disappearing. Returns "" only when there is truly nothing to show.
func sanitizeKubectlFailureOutput(raw string) string {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(klogFramePattern.ReplaceAllString(lines[i], ""))
		if line == "" {
			continue
		}
		if match := klogUnhandledErrorPattern.FindStringSubmatch(line); match != nil {
			return strings.ReplaceAll(match[1], `\"`, `"`)
		}
		return line
	}
	return ""
}

// KubernetesDeploymentAbsentMessageMarker is the fixed substring every
// "runtime ... is not deployed" message carries when a caller has confirmed
// a deployment is genuinely absent (checkKubernetesDeploymentWithContext
// returning (false, _, nil) above; erun-cli/cmd/open.go's
// ensureRuntimeDeployed builds the actual message). A caller that only has
// the rendered error text — for example the desktop reading a CLI
// subprocess's stderr — uses this marker to recognize that specific outcome
// without re-deriving kubectl's own error grammar, and without mistaking a
// check that could not resolve an answer (never contains this marker) for a
// real absence.
const KubernetesDeploymentAbsentMessageMarker = "is not deployed (deployment"

// isKubernetesContextMissingMessage matches the family of kubectl
// error messages that indicate the requested --context name is not
// in the kubeconfig. kubectl reports this as:
//
//	error: context "<name>" does not exist
//	context "<name>" does not exist
//	no context exists with the name: "<name>"
//
// "does not exist" is the stable signal across kubectl versions;
// "no context exists with the name" is the older shape that some
// distributions still ship. Match case-insensitively.
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

func hasExpectedDeploymentSettings(params KubernetesDeploymentCheckParams) bool {
	return strings.TrimSpace(params.ExpectedRepoPath) != "" ||
		params.ExpectedSSHD != nil ||
		params.ExpectedMCPPort > 0 ||
		params.ExpectedSSHPort > 0 ||
		params.ExpectedRuntimePod != (configpkg.RuntimePodResources{})
}

type deploymentEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func deploymentMatchesExpectedSettings(ctx core.Context, params KubernetesDeploymentCheckParams) (bool, error) {
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
		if sanitized := sanitizeKubectlFailureOutput(string(output)); sanitized != "" {
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
							Limits configpkg.RuntimePodResources `json:"limits"`
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
	var runtimeLimits configpkg.RuntimePodResources
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

// expectedDeploymentMatches seeds the per-field match state. Each field
// starts true when the corresponding expectation is unset (so a caller
// that didn't ask about it doesn't gate the result), and false otherwise
// — apply() flips it back to true on the first container env var that
// confirms the expected value.
//
// ERUN_API_PORT is deliberately not part of this matcher: the erun-api
// service is a separate Kubernetes deployment with its own port-forward
// path and presence check. Tenant-owned <tenant>-devops charts can
// legitimately omit ERUN_API_PORT from the runtime pod, and demanding it
// here would force a redeploy on every open for those tenants.
func expectedDeploymentMatches(params KubernetesDeploymentCheckParams) deploymentExpectedMatches {
	return deploymentExpectedMatches{
		repoPath:   strings.TrimSpace(params.ExpectedRepoPath) == "",
		sshd:       params.ExpectedSSHD == nil,
		mcpPort:    params.ExpectedMCPPort <= 0,
		sshPort:    params.ExpectedSSHPort <= 0,
		runtimePod: params.ExpectedRuntimePod == (configpkg.RuntimePodResources{}),
	}
}

func (m *deploymentExpectedMatches) apply(params KubernetesDeploymentCheckParams, name, value string) {
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
	return strings.EqualFold(strings.TrimSpace(value), formatHelmBool(*expected))
}

func matchesExpectedPort(value string, expected int) bool {
	if expected <= 0 {
		return true
	}
	return strings.TrimSpace(value) == fmt.Sprintf("%d", expected)
}

func matchesExpectedRuntimePod(value, expected configpkg.RuntimePodResources) bool {
	if expected == (configpkg.RuntimePodResources{}) {
		return true
	}
	value = configpkg.NormalizeRuntimePodResources(value)
	expected = configpkg.NormalizeRuntimePodResources(expected)
	valueCPU, valueCPUErr := configpkg.ParseKubernetesCPUToMilli(value.CPU)
	expectedCPU, expectedCPUErr := configpkg.ParseKubernetesCPUToMilli(expected.CPU)
	valueMemory, valueMemoryErr := configpkg.ParseKubernetesMemoryToMi(value.Memory)
	expectedMemory, expectedMemoryErr := configpkg.ParseKubernetesMemoryToMi(expected.Memory)
	return valueCPUErr == nil &&
		expectedCPUErr == nil &&
		valueMemoryErr == nil &&
		expectedMemoryErr == nil &&
		valueCPU == expectedCPU &&
		valueMemory == expectedMemory
}

func resolveKubernetesDeployValuesFile(chartPath, environment string) (string, error) {
	valuesFilePath := filepath.Join(chartPath, fmt.Sprintf("values.%s.yaml", strings.ToLower(strings.TrimSpace(environment))))
	info, err := os.Stat(valuesFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("values file not found for environment %q: %s", environment, valuesFilePath)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("values file path is a directory: %s", valuesFilePath)
	}
	return valuesFilePath, nil
}
