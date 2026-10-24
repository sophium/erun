package observe

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// computeObserveDrift diffs the live release and its pods against the env config. It returns
// non-nil (possibly empty) after a read and nil in dry-run; see ObserveResult.Drift.
func computeObserveDrift(req config.ShellLaunchParams, release *model.ObservedHelmRelease, pods []model.ObservedPod) []string {
	if release == nil {
		return nil
	}

	findings := []string{}
	recordedVersion := strings.TrimSpace(req.RuntimeVersion)

	// Found is false only when the helm read failed; Error distinguishes confirmed absence from
	// an unreadable release. The other drift checks still run.
	switch {
	case !release.Found && release.Error != "":
		findings = append(findings, unreadableReleaseDrift(req, release, recordedVersion)...)
	case !release.Found:
		findings = append(findings, missingReleaseDrift(req, release, recordedVersion)...)
	default:
		findings = append(findings, foundReleaseDrift(req, release, recordedVersion)...)
	}

	findings = append(findings, runningImageDrift(release, pods)...)
	findings = append(findings, runtimePodDrift(req, release)...)

	return findings
}

func unreadableReleaseDrift(req config.ShellLaunchParams, release *model.ObservedHelmRelease, recordedVersion string) []string {
	if recordedVersion == "" {
		return nil
	}
	return []string{fmt.Sprintf(
		"env config records runtimeversion %s but the runtime helm release %q in namespace %q could not be read: %s",
		recordedVersion, release.Name, req.Namespace, release.Error)}
}

func missingReleaseDrift(req config.ShellLaunchParams, release *model.ObservedHelmRelease, recordedVersion string) []string {
	if recordedVersion == "" {
		return nil
	}
	return []string{fmt.Sprintf(
		"env config records runtimeversion %s but no runtime helm release %q was found in namespace %q",
		recordedVersion, release.Name, req.Namespace)}
}

func foundReleaseDrift(req config.ShellLaunchParams, release *model.ObservedHelmRelease, recordedVersion string) []string {
	var findings []string
	if recordedVersion != "" && release.AppVersion != "" && recordedVersion != release.AppVersion {
		findings = append(findings, fmt.Sprintf(
			"env config runtimeversion (%s) does not match the release's app version (%s)",
			recordedVersion, release.AppVersion))
	}

	if runtimeImage := strings.TrimSpace(req.RuntimeImage); runtimeImage != "" {
		if recorded, ok := release.ImageOverrides[config.DevopsComponentName]; ok && recorded != runtimeImage {
			findings = append(findings, fmt.Sprintf(
				"env config runtimeimage (%s) does not match the release's imageOverrides.%s (%s)",
				runtimeImage, config.DevopsComponentName, recorded))
		}
	}
	return findings
}

// runningImageDrift flags an imageOverrides entry whose same-named container runs another image.
func runningImageDrift(release *model.ObservedHelmRelease, pods []model.ObservedPod) []string {
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

// runtimePodDrift compares only fields the env config recorded, never a manufactured default.
func runtimePodDrift(req config.ShellLaunchParams, release *model.ObservedHelmRelease) []string {
	if release.RuntimePod == (model.RuntimePodResources{}) {
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
