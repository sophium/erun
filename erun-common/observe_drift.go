package eruncommon

import (
	"fmt"
	"sort"
	"strings"
)

// computeObserveDrift diffs the live release and the pods it produced against
// what the env config records, so a caller sees a named disagreement instead
// of two dumps it must compare by eye. This is the read the orchestrator
// contract requires before any env-shaping deploy: "read the live release and
// diff it against the plan". release is nil in dry-run, where nothing was
// read yet; the drift list is empty in that case.
func computeObserveDrift(req ShellLaunchParams, release *ObservedHelmRelease, pods []ObservedPod) []string {
	if release == nil {
		return nil
	}

	var findings []string
	recordedVersion := strings.TrimSpace(req.RuntimeVersion)

	if !release.Found {
		if recordedVersion != "" {
			findings = append(findings, fmt.Sprintf(
				"env config records runtimeversion %s but no runtime helm release %q was found in namespace %q",
				recordedVersion, release.Name, req.Namespace))
		}
		return findings
	}

	if recordedVersion != "" && release.AppVersion != "" && recordedVersion != release.AppVersion {
		findings = append(findings, fmt.Sprintf(
			"env config runtimeversion (%s) does not match the release's app version (%s)",
			recordedVersion, release.AppVersion))
	}

	if runtimeImage := strings.TrimSpace(req.RuntimeImage); runtimeImage != "" {
		if recorded, ok := release.ImageOverrides[DevopsComponentName]; ok && recorded != runtimeImage {
			findings = append(findings, fmt.Sprintf(
				"env config runtimeimage (%s) does not match the release's imageOverrides.%s (%s)",
				runtimeImage, DevopsComponentName, recorded))
		}
	}

	findings = append(findings, runningImageDrift(release, pods)...)
	findings = append(findings, runtimePodDrift(req, release)...)

	return findings
}

// runningImageDrift flags any release-recorded imageOverrides entry whose
// component name matches a running container by name but not by image — the
// exact class the runtime chart's own container-naming convention makes
// possible (a container is named after its imageOverrides key, e.g.
// "erun-devops"), and the exact finding #1448 exists for: a helm release can
// name one image while the cluster runs another, hand-patched one.
func runningImageDrift(release *ObservedHelmRelease, pods []ObservedPod) []string {
	if len(release.ImageOverrides) == 0 {
		return nil
	}
	runningImages := make(map[string]string)
	for _, pod := range pods {
		for _, container := range pod.Containers {
			if container.Image != "" {
				runningImages[container.Name] = container.Image
			}
		}
	}

	keys := make([]string, 0, len(release.ImageOverrides))
	for key := range release.ImageOverrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var findings []string
	for _, key := range keys {
		recorded := release.ImageOverrides[key]
		running, ok := runningImages[key]
		if !ok || recorded == "" || running == recorded {
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"release records imageOverrides.%s = %s but the running container %q's image is %s",
			key, recorded, key, running))
	}
	return findings
}

// runtimePodDrift compares the env config's recorded runtime pod sizing
// against what the release actually rendered. Both sides are the release's
// own record of intent, not a live cgroup read, so this stays a plain
// `helm status` comparison rather than reaching into the pod.
func runtimePodDrift(req ShellLaunchParams, release *ObservedHelmRelease) []string {
	if release.RuntimePod == (RuntimePodResources{}) {
		return nil
	}
	recorded := NormalizeRuntimePodResources(req.RuntimePod)
	live := release.RuntimePod
	var findings []string
	if recorded.CPU != live.CPU {
		findings = append(findings, fmt.Sprintf(
			"env config runtimepod CPU limit (%s) does not match the release's runtime.resources.limits.cpu (%s)",
			recorded.CPU, live.CPU))
	}
	if recorded.Memory != live.Memory {
		findings = append(findings, fmt.Sprintf(
			"env config runtimepod memory limit (%s) does not match the release's runtime.resources.limits.memory (%s)",
			recorded.Memory, live.Memory))
	}
	return findings
}
