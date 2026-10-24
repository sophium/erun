package build

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// verifyDockerBuildPlatforms fails fast when binfmt/QEMU is missing for a required architecture.
func verifyDockerBuildPlatforms(required []string) error {
	available, err := dockerBuildxPlatforms()
	if err != nil {
		return err
	}
	// An unparseable platform list proves nothing; don't block a build that may succeed.
	if len(available) == 0 {
		return nil
	}
	missing := missingBuildPlatforms(required, available)
	if len(missing) == 0 {
		return nil
	}
	return newUnsupportedBuildPlatformError(required, missing)
}

func dockerBuildxPlatforms() (map[string]bool, error) {
	cmd := core.Command("docker", "buildx", "inspect")
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

// One Platforms: line per builder node; buildx marks a node's default with a trailing `*`.
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
