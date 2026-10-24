package build

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// materializeConfiguredFingerprints pulls the fp-tagged images configured fingerprints
// name so a fresh checkout can promote them; failures fall through to a rebuild.
func materializeConfiguredFingerprints(ctx core.Context, builds []seam.DockerBuildSpec) (map[string]struct{}, error) {
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
	cfg, _, err := config.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			cache[key] = map[string]string{}
			return cache[key], nil
		}
		return nil, err
	}
	configured := cfg.DockerFingerprintsForEnvironment(environment)
	cache[key] = configured
	return configured, nil
}

func materializeBuildFingerprints(ctx core.Context, build seam.DockerBuildSpec, hash string, materialized map[string]struct{}) {
	for _, platform := range build.Platforms {
		fpTag := dockerspec.FingerprintTag(build.Image, hash, platform)
		if exists, err := dockerregistry.DockerImageExists(fpTag); err == nil && exists {
			materialized[fpTag] = struct{}{}
			continue
		}
		if err := pullAndTagConfiguredFingerprint(ctx, build.Image.Tag, fpTag, platform, build.Image.Insecure); err != nil {
			ctx.Trace(fmt.Sprintf("could not materialize configured fingerprint %s: %v", fpTag, err))
			continue
		}
		materialized[fpTag] = struct{}{}
	}
}

func pullAndTagConfiguredFingerprint(ctx core.Context, sourceTag, fpTag, platform string, insecure bool) error {
	ctx.TraceCommand("", "docker", append(dockerspec.DockerManifestArgs("inspect", insecure), sourceTag)...)
	if !ctx.DryRun {
		exists, err := dockerregistry.DockerManifestExists(sourceTag, insecure)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("manifest not found for %s", sourceTag)
		}
	}
	ctx.TraceCommand("", "docker", "pull", "--platform", platform, sourceTag)
	if !ctx.DryRun {
		cmd := core.Command("docker", "pull", "--platform", platform, sourceTag)
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

// isValidFingerprintHash must match computeBuildFingerprint's output shape, so a mistyped hash is rejected.
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
