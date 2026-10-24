package dockerspec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

const FingerprintTagPrefix = "fp-"

// DockerManifestArgs needs --insecure spelled out because `docker manifest`
// talks to the registry directly, ignoring the daemon's insecure list.
func DockerManifestArgs(sub string, insecure bool) []string {
	args := []string{"manifest", sub}
	if insecure {
		args = append(args, "--insecure")
	}
	return args
}

// StableBaseVersionTag returns the unsuffixed local tag a wrapper resolves from
// `FROM image:${ERUN_VERSION}`.
func StableBaseVersionTag(image model.DockerImageReference) string {
	baseVersion := strings.TrimSpace(image.BaseVersion)
	if baseVersion == "" {
		return ""
	}
	registry := strings.TrimSpace(image.Registry)
	repo := image.ImageName
	if registry != "" {
		repo = strings.TrimRight(registry, "/") + "/" + image.ImageName
	}
	tag := repo + ":" + baseVersion
	if tag == strings.TrimSpace(image.Tag) {
		return ""
	}
	return tag
}

func FingerprintTag(image model.DockerImageReference, fingerprint, platform string) string {
	registry := strings.TrimSpace(image.Registry)
	repo := image.ImageName
	if registry != "" {
		repo = strings.TrimRight(registry, "/") + "/" + image.ImageName
	}
	suffix := FingerprintTagPrefix + fingerprint
	if platform != "" {
		suffix = suffix + "-" + platformShortSuffix(platform)
	}
	return repo + ":" + suffix
}

func PlatformSuffixedTag(tag, platform string) string {
	return tag + "-" + platformShortSuffix(platform)
}

func platformShortSuffix(platform string) string {
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return strings.ReplaceAll(platform, "/", "-")
}

func DockerBuildArgs(buildInput DockerBuildSpec, platform string) []string {
	tag := PlatformSuffixedTag(strings.TrimSpace(buildInput.Image.Tag), platform)
	// Provenance would make each per-arch tag a manifest list, which
	// `docker manifest create` rejects.
	args := []string{"build", "--platform", platform, "--provenance=false"}
	// Never --quiet: it suppresses a failing step's output at the source.
	args = append(args, "--progress=plain")
	args = append(args, dockerBuildEntitlementArgs(buildInput)...)
	args = append(args, "-t", tag)
	buildArgVersion := DockerBuildArgVersion(buildInput)
	// A local base resolves only under its per-arch tags; the arch-less tag
	// names just the last arch built.
	baseIsLocal := strings.TrimSpace(buildInput.LocalBaseTag) != "" || buildInput.Image.BaseVersion != ""
	if baseIsLocal && buildArgVersion != "" && dockerfileHasVersionedFrom(buildInput.DockerfilePath) {
		buildArgVersion = buildArgVersion + "-" + platformShortSuffix(platform)
	}
	if buildArgVersion != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+buildArgVersion)
	}
	if buildInput.DindCPULimit != "" {
		args = append(args, "--build-arg", "DIND_CPU_LIMIT="+buildInput.DindCPULimit)
	}
	if buildInput.DindMemoryLimitMiB != "" {
		args = append(args, "--build-arg", "DIND_MEMORY_LIMIT_MIB="+buildInput.DindMemoryLimitMiB)
	}
	if buildInput.CgroupParent != "" {
		args = append(args, "--cgroup-parent", buildInput.CgroupParent)
	}
	args = append(args, DockerSecretArgs(buildInput.DockerSecrets)...)
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

// dockerBuildEntitlementArgs grants network.host only to Dockerfiles with a
// `test` stage, which need it for container fixtures; it exposes the pod's
// network, including the unauthenticated dind listener.
func dockerBuildEntitlementArgs(buildInput DockerBuildSpec) []string {
	if !DockerfileDeclaresTestStage(buildInput.DockerfilePath) {
		return nil
	}
	return []string{"--allow", "network.host"}
}

// DockerBuildArgVersion is shared with the fingerprint so the cache identity
// and the baked-in version cannot disagree.
func DockerBuildArgVersion(buildInput DockerBuildSpec) string {
	if base := strings.TrimSpace(buildInput.Image.BaseVersion); base != "" {
		return base
	}
	return DockerImageTagVersion(strings.TrimSpace(buildInput.Image.Tag))
}

func DockerPushArgs(tag string, verbosity int) []string {
	if verbosity >= core.VerbosityDebug {
		return []string{"push", tag}
	}
	return []string{"push", "--quiet", tag}
}

func DockerImageTagVersion(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	index := strings.LastIndex(tag, ":")
	if index < 0 || index == len(tag)-1 {
		return ""
	}
	return tag[index+1:]
}
