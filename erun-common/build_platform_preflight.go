package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// verifyDockerBuildPlatforms checks the local Docker daemon can emit every
// required build platform before the multi-arch build shells `docker build
// --platform` once per architecture. erun always builds linux/amd64 +
// linux/arm64, so on a host that has not registered binfmt/QEMU for the foreign
// architecture the per-platform `docker build` otherwise fails with an opaque,
// low-level error. This preflight fails fast instead, with a direct, actionable
// message naming the unbuildable platform(s) and the binfmt remediation — the
// same `tonistiigi/binfmt --install all` step the runtime pod's init container
// uses. (Root AGENTS.md § "Release Rules": multi-architecture builds must
// verify daemon capability explicitly; issue #645.)
func verifyDockerBuildPlatforms(required []string) error {
	available, err := dockerBuildxPlatforms()
	if err != nil {
		return err
	}
	// If the probe reported no parseable platform list, we cannot prove any
	// platform is unsupported, so don't block the build. The missing-binfmt
	// case this preflight targets surfaces as a populated list that omits the
	// foreign arch (caught below); an empty or unrecognized inspect output
	// (an unusual builder, a future format change) degrades gracefully to the
	// prior behavior rather than failing a build that would have succeeded.
	if len(available) == 0 {
		return nil
	}
	missing := missingBuildPlatforms(required, available)
	if len(missing) == 0 {
		return nil
	}
	return newUnsupportedBuildPlatformError(missing)
}

// dockerBuildxPlatforms probes the current Docker builder for the platforms it
// can build for. The platform list reflects the kernel's registered binfmt
// handlers, so it is a faithful answer to "can `docker build --platform X`
// emulate X here". A failure to run the probe (no buildx, daemon down) is
// surfaced directly rather than letting the build fail later with a confusing
// per-platform error.
func dockerBuildxPlatforms() (map[string]bool, error) {
	cmd := Command("docker", "buildx", "inspect")
	out := new(bytes.Buffer)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(out.String())
		if message == "" {
			return nil, fmt.Errorf("verify multi-arch build capability (docker buildx inspect): %w", err)
		}
		return nil, fmt.Errorf("verify multi-arch build capability (docker buildx inspect): %w\n%s", err, message)
	}
	return parseBuildxPlatforms(out.String()), nil
}

// parseBuildxPlatforms extracts the platform set from `docker buildx inspect`
// output. The relevant line looks like:
//
//	Platforms: linux/arm64*, linux/amd64, linux/amd64/v2, linux/arm/v7
//
// buildx marks the node's default platform with a trailing `*`, which is
// stripped. Multiple `Platforms:` lines (one per builder node) are unioned so a
// multi-node builder reports the full reachable set.
func parseBuildxPlatforms(inspectOutput string) map[string]bool {
	platforms := make(map[string]bool)
	for _, line := range strings.Split(inspectOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Platforms:") {
			continue
		}
		list := strings.TrimSpace(strings.TrimPrefix(trimmed, "Platforms:"))
		for _, token := range strings.Split(list, ",") {
			token = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(token), "*"))
			if token != "" {
				platforms[token] = true
			}
		}
	}
	return platforms
}

// missingBuildPlatforms returns the required platforms the daemon cannot build,
// preserving the required-list order so the error message is deterministic.
func missingBuildPlatforms(required []string, available map[string]bool) []string {
	var missing []string
	for _, platform := range required {
		if !available[platform] {
			missing = append(missing, platform)
		}
	}
	return missing
}

// newUnsupportedBuildPlatformError builds the actionable error returned when the
// daemon cannot produce a required platform. It names the missing platform(s),
// states the full set erun builds, and gives the exact binfmt-install command.
func newUnsupportedBuildPlatformError(missing []string) error {
	return fmt.Errorf(
		"the local Docker daemon cannot build %s: no emulator is registered for the foreign architecture. "+
			"erun always builds %s, so the build cannot proceed. Register binfmt/QEMU for the missing platform(s) and retry:\n"+
			"  docker run --privileged --rm tonistiigi/binfmt --install all",
		strings.Join(missing, ", "),
		strings.Join(multiPlatformDockerBuilds, " + "),
	)
}
