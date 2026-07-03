package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// materializeConfiguredFingerprints seeds the local Docker store with the
// fp-tagged images a project's configured fingerprints point at, so a fresh
// checkout can promote those builds instead of rebuilding them. Failures fall
// through silently, leaving the build to rebuild as it would without this step.
func materializeConfiguredFingerprints(ctx Context, builds []DockerBuildSpec) (map[string]struct{}, error) {
	materialized := make(map[string]struct{})
	configuredByEnv := make(map[string]map[string]string)

	for _, build := range builds {
		projectRoot := strings.TrimSpace(build.Image.ProjectRoot)
		environment := strings.TrimSpace(build.Image.Environment)
		imageName := strings.TrimSpace(build.Image.ImageName)
		if projectRoot == "" || imageName == "" {
			continue
		}
		configured, err := loadConfiguredFingerprints(projectRoot, environment, configuredByEnv)
		if err != nil {
			return nil, err
		}
		if configured == nil {
			continue
		}
		hash, ok := configured[imageName]
		if !ok {
			continue
		}
		if !isValidFingerprintHash(hash) {
			ctx.Trace(fmt.Sprintf("ignoring invalid configured fingerprint for %s: %q", imageName, hash))
			continue
		}
		materializeBuildFingerprints(ctx, build, hash, materialized)
	}

	return materialized, nil
}

func loadConfiguredFingerprints(projectRoot, environment string, cache map[string]map[string]string) (map[string]string, error) {
	key := projectRoot + "\x00" + environment
	if configured, loaded := cache[key]; loaded {
		return configured, nil
	}
	cfg, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			cache[key] = nil
			return nil, nil
		}
		return nil, err
	}
	configured := cfg.DockerFingerprintsForEnvironment(environment)
	cache[key] = configured
	return configured, nil
}

func materializeBuildFingerprints(ctx Context, build DockerBuildSpec, hash string, materialized map[string]struct{}) {
	for _, platform := range build.Platforms {
		fpTag := fingerprintTag(build.Image, hash, platform)
		if exists, err := DockerImageExists(fpTag); err == nil && exists {
			materialized[fpTag] = struct{}{}
			continue
		}
		if err := pullAndTagConfiguredFingerprint(ctx, build.Image.Tag, fpTag, platform); err != nil {
			ctx.Trace(fmt.Sprintf("could not materialize configured fingerprint %s: %v", fpTag, err))
			continue
		}
		materialized[fpTag] = struct{}{}
	}
}

func pullAndTagConfiguredFingerprint(ctx Context, sourceTag, fpTag, platform string) error {
	ctx.TraceCommand("", "docker", "manifest", "inspect", sourceTag)
	if !ctx.DryRun {
		exists, err := DockerManifestExists(sourceTag)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("manifest not found for %s", sourceTag)
		}
	}
	ctx.TraceCommand("", "docker", "pull", "--platform", platform, sourceTag)
	if !ctx.DryRun {
		cmd := Command("docker", "pull", "--platform", platform, sourceTag)
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	ctx.TraceCommand("", "docker", "tag", sourceTag, fpTag)
	if !ctx.DryRun {
		if err := runDockerTag(sourceTag, fpTag, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// isValidFingerprintHash mirrors computeBuildFingerprint's output shape, so a
// mistyped configured hash is rejected rather than promoted as if it were real.
func isValidFingerprintHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 16 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
