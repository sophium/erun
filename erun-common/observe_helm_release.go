package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ObservedHelmRelease is the runtime helm release's own record of what should
// be running — the deployed counterpart to a deploy plan's HelmDeploySpec.
// Found distinguishes "no release exists here" from a read that failed: a
// caller must never treat an empty release as "nothing deployed" when the
// truth is "observe could not look" (see Error).
type ObservedHelmRelease struct {
	Name           string              `json:"name"`
	Found          bool                `json:"found"`
	Revision       int                 `json:"revision,omitempty"`
	Status         string              `json:"status,omitempty"`
	Chart          string              `json:"chart,omitempty"`
	ChartVersion   string              `json:"chartVersion,omitempty"`
	AppVersion     string              `json:"appVersion,omitempty"`
	ImageOverrides map[string]string   `json:"imageOverrides,omitempty"`
	RuntimePod     RuntimePodResources `json:"runtimePod,omitempty"`
	// Error explains why a field above could not be populated: insufficient
	// RBAC, an unreachable cluster, or a release whose chart does not look
	// like erun's runtime chart. Empty means the read above is trustworthy.
	Error string `json:"error,omitempty"`
}

// observeHelmStatusArgs builds a read-only `helm status <release> -o json`
// invocation, mirroring doctor's helmStatusArgs (doctor_deploy.go) with a JSON
// output flag added so observe can parse it instead of only displaying it.
func observeHelmStatusArgs(req ShellLaunchParams) []string {
	args := []string{"status", RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "-o", "json")
}

// helmStatusOutput is a deliberately partial parse of `helm status -o json`,
// matching the podStatusList idiom elsewhere in observe: unknown fields are
// ignored so a helm version's extra output does not break observe. Config is
// the release's own merged --set/-f values (not the chart's computed
// defaults), which is exactly "the values erun itself sets" the runtime chart
// always passes explicitly (deploy.go's imageOverrides.* / runtime.resources.
// limits.* --set-string args), including when a wrapping umbrella nests them
// under its subchart key.
type helmStatusOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Info      struct {
		Status string `json:"status"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			AppVersion string `json:"appVersion"`
		} `json:"metadata"`
	} `json:"chart"`
	Config map[string]interface{} `json:"config"`
}

// fetchObservedHelmRelease reads the runtime release's status. A missing
// release and a failed read are both reported as Found: false, distinguished
// by Error: a missing release means "confirmed nothing deployed", a non-empty
// Error means "could not tell" plus what to do about it.
func fetchObservedHelmRelease(args []string, releaseName, namespace string) *ObservedHelmRelease {
	raw, stderr, err := runObserveHelm(args)
	if err != nil {
		if isHelmReleaseNotFound(stderr) {
			return &ObservedHelmRelease{Name: releaseName}
		}
		return &ObservedHelmRelease{Name: releaseName, Error: observeHelmReadErrorMessage(releaseName, namespace, stderr, err)}
	}
	var status helmStatusOutput
	if jsonErr := json.Unmarshal(raw, &status); jsonErr != nil {
		return &ObservedHelmRelease{Name: releaseName, Error: fmt.Sprintf("observe: could not parse helm status for release %q: %v", releaseName, jsonErr)}
	}
	release := &ObservedHelmRelease{
		Name:         status.Name,
		Found:        true,
		Revision:     status.Version,
		Status:       status.Info.Status,
		Chart:        status.Chart.Metadata.Name,
		ChartVersion: status.Chart.Metadata.Version,
		AppVersion:   status.Chart.Metadata.AppVersion,
	}
	if overrides := findNestedStringMap(status.Config, "imageOverrides"); overrides != nil {
		release.ImageOverrides = overrides
	}
	release.RuntimePod = findRuntimePodResourceLimits(status.Config)
	if release.Chart != "" && !strings.Contains(strings.ToLower(release.Chart), "devops") {
		release.Error = fmt.Sprintf("release %q's chart is %q, which does not look like an erun runtime chart (expected one named like %q) — verify with 'helm get chart %s'", release.Name, release.Chart, DevopsComponentName, release.Name)
	}
	return release
}

// runObserveHelm runs a read-only `helm` invocation, matching
// runObserveKubectl's stdout/stderr split so callers can classify failures.
func runObserveHelm(args []string) (stdout []byte, stderr string, err error) {
	cmd := Command("helm", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

func isHelmReleaseNotFound(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "release: not found")
}

// observeHelmReadErrorMessage classifies a failed helm read into an actionable
// message: an orchestrator reading "the release cannot be read" with no reason
// has nowhere to go, per root AGENTS.md's "Smooth, Seamless, No Dead Ends".
func observeHelmReadErrorMessage(releaseName, namespace, stderr string, err error) string {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "forbidden") || strings.Contains(lower, "unauthorized"):
		return fmt.Sprintf("observe: not authorized to read helm release %q in namespace %q (%s) — helm release state is stored as Secrets, so the caller's kubeconfig needs read access to Secrets in this namespace", releaseName, namespace, stderr)
	case strings.Contains(strings.ToLower(err.Error()), "executable file not found"):
		return "observe: helm is not installed or not on PATH — install helm to let observe read the runtime release"
	case stderr != "":
		return fmt.Sprintf("observe: could not read helm release %q in namespace %q: %s", releaseName, namespace, stderr)
	default:
		return fmt.Sprintf("observe: could not read helm release %q in namespace %q: %v", releaseName, namespace, err)
	}
}

// findNestedStringMap searches values for a key whose value is a map of
// strings, at any depth. The runtime chart's imageOverrides can live at the
// top level (a chart installed directly) or nested under a wrapping umbrella's
// subchart key (deploy.go's prefixHelmSetKeys) — this walk finds it either
// way without observe needing to know which umbrella wrapped it.
func findNestedStringMap(values map[string]interface{}, key string) map[string]string {
	if v, ok := values[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return stringifyMap(m)
		}
	}
	for _, v := range values {
		if child, ok := v.(map[string]interface{}); ok {
			if found := findNestedStringMap(child, key); found != nil {
				return found
			}
		}
	}
	return nil
}

func stringifyMap(m map[string]interface{}) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// findRuntimePodResourceLimits searches values for the runtime chart's own
// "runtime.resources.limits.{cpu,memory}" shape, at any depth, the same way
// findNestedStringMap does for imageOverrides.
func findRuntimePodResourceLimits(values map[string]interface{}) RuntimePodResources {
	if v, ok := values["runtime"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			if resources := runtimePodResourcesFromRuntimeValue(m); resources != (RuntimePodResources{}) {
				return resources
			}
		}
	}
	for _, v := range values {
		if child, ok := v.(map[string]interface{}); ok {
			if resources := findRuntimePodResourceLimits(child); resources != (RuntimePodResources{}) {
				return resources
			}
		}
	}
	return RuntimePodResources{}
}

func runtimePodResourcesFromRuntimeValue(runtime map[string]interface{}) RuntimePodResources {
	resources, ok := runtime["resources"].(map[string]interface{})
	if !ok {
		return RuntimePodResources{}
	}
	limits, ok := resources["limits"].(map[string]interface{})
	if !ok {
		return RuntimePodResources{}
	}
	cpu, _ := limits["cpu"].(string)
	memory, _ := limits["memory"].(string)
	return RuntimePodResources{CPU: cpu, Memory: memory}
}
