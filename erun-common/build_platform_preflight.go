package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// verifyDockerBuildPlatforms fails fast when the host lacks binfmt/QEMU for a
// required architecture, so the multi-arch build surfaces an actionable message
// instead of the opaque error a per-platform `docker build` would otherwise emit.
func verifyDockerBuildPlatforms(required []string) error {
	available, err := dockerBuildxPlatforms()
	if err != nil {
		return err
	}
	// An empty/unparseable platform list cannot prove anything is unsupported, so
	// degrade to the prior behavior rather than block a build that would succeed.
	if len(available) == 0 {
		return nil
	}
	missing := missingBuildPlatforms(required, available)
	if len(missing) == 0 {
		return nil
	}
	return newUnsupportedBuildPlatformError(required, missing)
}

// dockerBuildxPlatforms probes the current builder; its platform list reflects
// the kernel's registered binfmt handlers, so it faithfully answers whether
// `docker build --platform X` can emulate X here.
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

// parseBuildxPlatforms unions the platforms across every `Platforms:` line (one
// per builder node) so a multi-node builder reports its full reachable set, and
// strips the trailing `*` buildx uses to mark a node's default platform.
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

// missingBuildPlatforms preserves the required-list order so the resulting error
// message is deterministic.
func missingBuildPlatforms(required []string, available map[string]bool) []string {
	var missing []string
	for _, platform := range required {
		if !available[platform] {
			missing = append(missing, platform)
		}
	}
	return missing
}

func newUnsupportedBuildPlatformError(required, missing []string) error {
	return fmt.Errorf(
		"the local Docker daemon cannot build %s: no emulator is registered for the foreign architecture. "+
			"this build targets %s, so the build cannot proceed. Register binfmt/QEMU for the missing platform(s) and retry:\n"+
			"  docker run --privileged --rm tonistiigi/binfmt --install all",
		strings.Join(missing, ", "),
		strings.Join(required, " + "),
	)
}
