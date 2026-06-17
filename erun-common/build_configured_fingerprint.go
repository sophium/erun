package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// materializeConfiguredFingerprints inspects each build's image name against
// the project's docker.fingerprints map for the build's environment and, when
// a configured fingerprint is found, ensures the local Docker store holds the
// fp-tagged image so applyIncrementalPromotion can promote the build instead
// of rebuilding it on a fresh checkout.
//
// For each build with a configured fingerprint, for each platform:
//   - if <image>:fp-<configured>-<arch> already exists locally → record it as
//     materialized; no docker work needed.
//   - otherwise → trace `docker manifest inspect`, `docker pull --platform`,
//     `docker tag` for the per-platform pull+tag sequence. In real run the
//     commands execute; in dry-run they are traced only and the resulting
//     fp-tag is still recorded as materialized so the downstream incremental
//     trace reflects the would-be promote path.
//
// Failures (registry not reachable, manifest missing, malformed configured
// hash, unknown image name) fall through silently: the fp-tag is not added to
// the materialized set and applyIncrementalPromotion will mark the build for
// rebuild as it would today.
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

// loadConfiguredFingerprints returns the docker.fingerprints map for the
// build's environment, caching the per-(projectRoot, environment) result in
// cache so repeated builds avoid re-reading the config. An uninitialized
// project caches a nil entry rather than erroring; any other config error
// propagates.
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

// materializeBuildFingerprints ensures the local Docker store holds the
// fp-tagged image for each of the build's platforms at the configured hash,
// recording each materialized fp-tag. An fp-tag already present is recorded
// directly; otherwise the pull+tag sequence runs (or is traced in dry-run).
// Per-platform failures fall through silently so the build rebuilds as today.
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

// pullAndTagConfiguredFingerprint pulls sourceTag for platform and tags the
// pulled image locally as fpTag. In dry-run the docker commands are traced
// but not executed, and a nil error is returned so the caller treats the
// fp-tag as materialized for the rest of the resolution.
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

// isValidFingerprintHash mirrors computeBuildFingerprint's output shape: a
// 16-character lowercase hex digest. Anything else is treated as a config
// typo and skipped (with a trace) rather than promoted.
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
