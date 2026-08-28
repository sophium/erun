package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
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

// observeHelmListArgs builds a read-only `helm list -o json` invocation
// filtered to exactly the runtime release. `helm status -o json` carries no
// chart metadata at all (its top-level keys are apply_method, config, info,
// manifest, name, namespace, version — no chart), so observe reads chart and
// appVersion from list instead, which does carry them.
func observeHelmListArgs(req ShellLaunchParams, releaseName string) []string {
	args := []string{"list", "--filter", "^" + regexp.QuoteMeta(releaseName) + "$"}
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
	Config map[string]interface{} `json:"config"`
}

// helmListEntry is a deliberately partial parse of one `helm list -o json`
// entry: chart (rendered as "<name>-<version>") and app_version are the
// fields helmStatusOutput cannot carry (see observeHelmListArgs).
type helmListEntry struct {
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

// helmChartNameVersionPattern splits a `helm list` chart string such as
// "erun-devops-1.0.206" into its name and semver-shaped version.
var helmChartNameVersionPattern = regexp.MustCompile(`^(.+)-(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.]+)*)$`)

func splitHelmChartNameVersion(chart string) (name, version string, ok bool) {
	match := helmChartNameVersionPattern.FindStringSubmatch(chart)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

// fetchObservedHelmRelease reads the runtime release's status. A missing
// release and a failed read are both reported as Found: false, distinguished
// by Error: a missing release means "confirmed nothing deployed", a non-empty
// Error means "could not tell" plus what to do about it.
func fetchObservedHelmRelease(args, listArgs []string, releaseName, namespace string) *ObservedHelmRelease {
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
		release.Error = appendObserveHelmReleaseError(release.Error, fmt.Sprintf("release %q's chart is %q, which does not look like an erun runtime chart (expected one named like %q) — verify with 'helm get chart %s'", release.Name, release.Chart, DevopsComponentName, release.Name))
	}
	return release
}

// populateObservedChartFields fills Chart/ChartVersion/AppVersion from `helm
// list`, the only one of observe's two helm reads that carries them. When
// that read genuinely cannot resolve them, it appends to release.Error
// instead of leaving Chart/ChartVersion/AppVersion at their zero value: an
// empty string there must never be read as "the release has no chart" when
// the truth is "observe could not look" — the same hazard Found/Error already
// guards against for the release as a whole.
func populateObservedChartFields(release *ObservedHelmRelease, listArgs []string, releaseName, namespace string) {
	raw, stderr, err := runObserveHelm(listArgs)
	if err != nil {
		release.Error = appendObserveHelmReleaseError(release.Error, "could not determine chart/appVersion: "+observeHelmReadErrorMessage(releaseName, namespace, stderr, err))
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
