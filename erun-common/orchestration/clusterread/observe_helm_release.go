package clusterread

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// ObservedHelmRelease is the runtime release's record; Found false with Error set means observe could not look.
type ObservedHelmRelease struct {
	Name           string                     `json:"name"`
	Found          bool                       `json:"found"`
	Revision       int                        `json:"revision,omitempty"`
	Status         string                     `json:"status,omitempty"`
	Chart          string                     `json:"chart,omitempty"`
	ChartVersion   string                     `json:"chartVersion,omitempty"`
	AppVersion     string                     `json:"appVersion,omitempty"`
	ImageOverrides map[string]string          `json:"imageOverrides,omitempty"`
	RuntimePod     config.RuntimePodResources `json:"runtimePod,omitempty"`
	// Error explains why a field could not be populated; empty means the read is trustworthy.
	Error string `json:"error,omitempty"`
}

// ObserveHelmStatusArgs builds `helm status <release> -o json`.
func ObserveHelmStatusArgs(req config.ShellLaunchParams) []string {
	args := []string{"status", config.RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "-o", "json")
}

// ObserveHelmListArgs builds `helm list -o json` for the runtime release; helm status carries no chart metadata.
func ObserveHelmListArgs(req config.ShellLaunchParams, releaseName string) []string {
	args := []string{"list", "--filter", "^" + regexp.QuoteMeta(releaseName) + "$"}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "-o", "json")
}

// helmStatusOutput is a partial parse of `helm status -o json`; Config is the release's
// user-supplied values, not chart defaults.
type helmStatusOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Info      struct {
		Status string `json:"status"`
	} `json:"info"`
	Config map[string]interface{} `json:"config"`
}

type helmListEntry struct {
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

var helmChartNameVersionPattern = regexp.MustCompile(`^(.+)-(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.]+)*)$`)

func splitHelmChartNameVersion(chart string) (name, version string, ok bool) {
	match := helmChartNameVersionPattern.FindStringSubmatch(chart)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

// FetchObservedHelmRelease reads the runtime release; Error distinguishes a failed read from a missing release.
func FetchObservedHelmRelease(args, listArgs []string, releaseName, namespace string) *ObservedHelmRelease {
	raw, stderr, err := runObserveHelm(args)
	if err != nil {
		if deploy.IsHelmReleaseNotFound(stderr) {
			return &ObservedHelmRelease{Name: releaseName}
		}
		return &ObservedHelmRelease{Name: releaseName, Error: deploy.ObserveHelmReadErrorMessage(releaseName, namespace, stderr, err)}
	}
	var status helmStatusOutput
	if jsonErr := json.Unmarshal(raw, &status); jsonErr != nil {
		return &ObservedHelmRelease{Name: releaseName, Error: fmt.Sprintf("observe: could not parse helm status for release %q: %v", releaseName, jsonErr)}
	}
	release := &ObservedHelmRelease{
		Name:     status.Name,
		Found:    true,
		Revision: status.Version,
		Status:   status.Info.Status,
	}
	if overrides := findNestedStringMap(status.Config, "imageOverrides"); overrides != nil {
		release.ImageOverrides = overrides
	}
	release.RuntimePod = findRuntimePodResourceLimits(status.Config)
	populateObservedChartFields(release, listArgs, releaseName, namespace)
	if release.Chart != "" && !strings.Contains(strings.ToLower(release.Chart), "devops") {
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("release %q's chart is %q, which does not look like an erun runtime chart (expected one named like %q) — verify with 'helm get chart %s'", release.Name, release.Chart, config.DevopsComponentName, release.Name))
	}
	return release
}

// populateObservedChartFields fills chart fields from `helm list`, recording in Error
// when it cannot rather than leaving them empty.
func populateObservedChartFields(release *ObservedHelmRelease, listArgs []string, releaseName, namespace string) {
	raw, stderr, err := runObserveHelm(listArgs)
	if err != nil {
		release.Error = appendObserveHelmReleaseError(release.Error, "could not determine chart/appVersion: "+deploy.ObserveHelmReadErrorMessage(releaseName, namespace, stderr, err))
		return
	}
	var entries []helmListEntry
	if jsonErr := json.Unmarshal(raw, &entries); jsonErr != nil {
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("observe: could not parse helm list output for release %q: %v", releaseName, jsonErr))
		return
	}
	if len(entries) == 0 {
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("helm status found release %q but helm list did not — chart/appVersion could not be determined", releaseName))
		return
	}
	entry := entries[0]
	release.AppVersion = entry.AppVersion
	if entry.Chart == "" {
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("helm list returned an empty chart field for release %q — chart/chartVersion could not be determined", releaseName))
		return
	}
	if name, version, ok := splitHelmChartNameVersion(entry.Chart); ok {
		release.Chart = name
		release.ChartVersion = version
	} else {
		release.Chart = entry.Chart
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("could not separate release %q's chart name from its version in %q — showing the combined value as chart", releaseName, entry.Chart))
	}
}

func appendObserveHelmReleaseError(existing, addition string) string {
	if addition == "" {
		return existing
	}
	if existing == "" {
		return addition
	}
	return existing + "; " + addition
}

func runObserveHelm(args []string) (stdout []byte, stderr string, err error) {
	cmd := core.Command("helm", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

// findNestedStringMap finds a string map under key at any depth, since an umbrella
// may nest the runtime chart's values under its subchart key.
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

func findRuntimePodResourceLimits(values map[string]interface{}) config.RuntimePodResources {
	if v, ok := values["runtime"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			if resources := runtimePodResourcesFromRuntimeValue(m); resources != (config.RuntimePodResources{}) {
				return resources
			}
		}
	}
	for _, v := range values {
		if child, ok := v.(map[string]interface{}); ok {
			if resources := findRuntimePodResourceLimits(child); resources != (config.RuntimePodResources{}) {
				return resources
			}
		}
	}
	return config.RuntimePodResources{}
}

func runtimePodResourcesFromRuntimeValue(runtime map[string]interface{}) config.RuntimePodResources {
	resources, ok := runtime["resources"].(map[string]interface{})
	if !ok {
		return config.RuntimePodResources{}
	}
	limits, ok := resources["limits"].(map[string]interface{})
	if !ok {
		return config.RuntimePodResources{}
	}
	cpu, _ := limits["cpu"].(string)
	memory, _ := limits["memory"].(string)
	return config.RuntimePodResources{CPU: cpu, Memory: memory}
}
