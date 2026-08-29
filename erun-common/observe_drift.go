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

	// release.Found is only false when the helm read itself failed (see
	// ObservedHelmRelease's doc comment); release.Error then distinguishes a
	// confirmed absence from a read that could not tell — a caller must never
	// see the same "not found" line for both. Either way, runningImageDrift
	// and runtimePodDrift below still run: their own inputs (release fields
	// populated only on a successful read) make them no-ops here today, but
	// nothing about this branch should stop them from evaluating whatever
	// they can.
	switch {
	case !release.Found && release.Error != "":
		if recordedVersion != "" {
			findings = append(findings, fmt.Sprintf(
				"env config records runtimeversion %s but the runtime helm release %q in namespace %q could not be read: %s",
				recordedVersion, release.Name, req.Namespace, release.Error))
		}
	case !release.Found:
		if recordedVersion != "" {
			findings = append(findings, fmt.Sprintf(
				"env config records runtimeversion %s but no runtime helm release %q was found in namespace %q",
				recordedVersion, release.Name, req.Namespace))
		}
	default:
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
// `helm status` comparison rather than reaching into the pod. Unlike deploy,
// which must resolve a concrete value to pass to helm and so normalizes an
// unset field to NormalizeRuntimePodResources' package default, a config field
// left empty here asserts nothing about the environment's shape — the same
// reasoning the recordedVersion/runtimeImage checks above already apply — so
// each field compares only when the config actually recorded one, instead of
// diffing the release against a manufactured default nobody configured.
func runtimePodDrift(req ShellLaunchParams, release *ObservedHelmRelease) []string {
	if release.RuntimePod == (RuntimePodResources{}) {
		return nil
	}
	recorded := req.RuntimePod
	live := release.RuntimePod
	var findings []string
	if cpu := strings.TrimSpace(recorded.CPU); cpu != "" && cpu != live.CPU {
		findings = append(findings, fmt.Sprintf(
			"env config runtimepod CPU limit (%s) does not match the release's runtime.resources.limits.cpu (%s)",
			cpu, live.CPU))
	}
	if memory := strings.TrimSpace(recorded.Memory); memory != "" && memory != live.Memory {
		findings = append(findings, fmt.Sprintf(
			"env config runtimepod memory limit (%s) does not match the release's runtime.resources.limits.memory (%s)",
			memory, live.Memory))
	}
	return findings
}
