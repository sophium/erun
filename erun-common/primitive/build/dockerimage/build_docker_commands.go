package dockerimage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/build/diagnose"
	"github.com/sophium/erun/erun-common/primitive/build/dockercli"
	"github.com/sophium/erun/erun-common/primitive/build/mtu"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func DockerImageBuilder(buildInput imagebuild.DockerBuildSpec, stdout, stderr io.Writer) error {
	if buildInput.Promote {
		return promoteDockerImage(buildInput, stdout, stderr)
	}
	return runMultiPlatformBuild(buildInput, stdout, stderr)
}

func runMultiPlatformBuild(buildInput imagebuild.DockerBuildSpec, stdout, stderr io.Writer) error {
	if err := verifyDockerBuildPlatforms(buildInput.Platforms); err != nil {
		return err
	}
	mtu.WarnAboutBridgeMTUMismatch(stderr)
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		cgroupBefore := core.CaptureBuildCgroupSnapshot()
		platformTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		output, err := buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
		elapsed := time.Since(started)
		if buildInput.PlatformObserver != nil {
			cgroup := core.BuildCgroupMetricsFromSnapshots(cgroupBefore, core.CaptureBuildCgroupSnapshot(), elapsed)
			buildInput.PlatformObserver(platform, elapsed, err, cgroup, output)
		}
		if err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return assembleMultiPlatformManifest(buildInput.Image.Tag, perPlatformTags, buildInput.Image.Insecure, buildInput.Verbosity, stdout, stderr)
}

// buildPlatformImageFromSource builds, tags and pushes one platform, returning the
// captured build output for step timing.
func buildPlatformImageFromSource(buildInput imagebuild.DockerBuildSpec, platform string, stdout, stderr io.Writer) (string, error) {
	platformTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	args := dockerspec.DockerBuildArgs(buildInput, platform)
	output, err := RunDockerBuildOnce(args, buildInput.ContextDir, buildInput.Image.Tag, false, buildInput.Verbosity, stdout, stderr)
	if err == nil {
		err = tagFingerprintAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	return output, err
}

func promoteDockerImage(buildInput imagebuild.DockerBuildSpec, stdout, stderr io.Writer) error {
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		platformTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		output, err := promotePlatformImage(buildInput, platform, stdout, stderr)
		if buildInput.PlatformObserver != nil {
			// Promotion never runs `docker build`, so there is no build cost to attribute.
			buildInput.PlatformObserver(platform, time.Since(started), err, nil, output)
		}
		if err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return assembleMultiPlatformManifest(buildInput.Image.Tag, perPlatformTags, buildInput.Image.Insecure, buildInput.Verbosity, stdout, stderr)
}

// promotePlatformImage re-tags and pushes one platform's cached fingerprint image,
// falling back to a real build when settlePromoteFailure deems the cache untrustworthy.
func promotePlatformImage(buildInput imagebuild.DockerBuildSpec, platform string, stdout, stderr io.Writer) (string, error) {
	fpTag := dockerspec.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	platformTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	err := dockercli.RunDockerTag(fpTag, platformTag, stdout, stderr)
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	singlePlatform := true
	if err == nil {
		// A promoted tag has no build behind it, so check it is not a multi-platform index,
		// which manifest assembly rejects.
		singlePlatform = localImageIsSinglePlatform(platformTag)
	}
	if err == nil && singlePlatform {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	if err == nil && singlePlatform {
		return "", nil
	}
	return settlePromoteFailure(buildInput, platform, platformTag, fpTag, err, stdout, stderr)
}

// promoteSourceMissing reports only an affirmative "no image"; a failed inspect is not evidence.
func promoteSourceMissing(fpTag string) bool {
	present, err := dockerregistry.DockerImageExists(fpTag)
	return err == nil && !present
}

// settlePromoteFailure rebuilds from source when the cached image vanished, the registry
// reports "unknown blob", or the image is not single-platform; other failures are returned.
func settlePromoteFailure(buildInput imagebuild.DockerBuildSpec, platform, platformTag, fpTag string, promoteErr error, stdout, stderr io.Writer) (string, error) {
	switch {
	case promoteErr == nil:
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s would publish a multi-platform image under a per-arch tag; rebuilding %s from source instead of trusting the cache\n", platformTag, fpTag, platform)
	case promoteSourceMissing(fpTag):
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s failed (%v); that cached image is no longer in the local image store, so rebuilding %s from source instead of trusting the cache\n", platformTag, fpTag, promoteErr, platform)
	case dockerregistry.IsDockerUnknownBlobError(promoteErr.Error()):
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s failed (%v); the registry does not have every blob it references, so rebuilding from source instead of trusting the cache\n", platformTag, fpTag, promoteErr)
	default:
		return "", fmt.Errorf("promote %s from cached fingerprint image %s: %w", platformTag, fpTag, promoteErr)
	}
	// The run now does real build work, so the timing row must not report a cache hit.
	buildInput.Cache.NotePromoteFallback()
	return buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
}

// localImageIsSinglePlatform reports false only for a readable multi-platform index
// descriptor; anything unreadable counts as single-platform.
func localImageIsSinglePlatform(tag string) bool {
	cmd := core.Command("docker", "image", "inspect", tag)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return true
	}
	var images []struct {
		Descriptor struct {
			MediaType string `json:"mediaType"`
		} `json:"Descriptor"`
	}
	if err := json.Unmarshal(output.Bytes(), &images); err != nil {
		return true
	}
	for _, image := range images {
		if isImageIndexMediaType(image.Descriptor.MediaType) {
			return false
		}
	}
	return true
}

func isImageIndexMediaType(mediaType string) bool {
	switch strings.TrimSpace(mediaType) {
	case "application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.index.v1+json":
		return true
	default:
		return false
	}
}

// pushPlatformImage publishes each platform as soon as it builds: the containerd store
// may collect tag-only content before a later push pass runs.
func pushPlatformImage(buildInput imagebuild.DockerBuildSpec, platformTag string, stdout, stderr io.Writer) error {
	if !buildInput.Push {
		return nil
	}
	return dockerregistry.DockerImagePusher(platformTag, buildInput.Verbosity, stdout, stderr)
}

// assembleMultiPlatformManifest publishes the arch-less tag. The cached local manifest
// list is removed first, or a re-publish would merge into it and keep stale digests.
func assembleMultiPlatformManifest(tag string, perPlatformTags []string, insecure bool, verbosity int, stdout, stderr io.Writer) error {
	// `docker manifest rm` is local-only, so it needs no --insecure.
	_ = dockercli.RunDockerSimpleCommandWithVerbosity([]string{"manifest", "rm", tag}, verbosity, io.Discard, io.Discard)
	createArgs := append(dockerspec.DockerManifestArgs("create", insecure), tag)
	createArgs = append(createArgs, perPlatformTags...)
	if err := dockercli.RunDockerSimpleCommandWithVerbosity(createArgs, verbosity, stdout, stderr); err != nil {
		return err
	}
	pushArgs := append(dockerspec.DockerManifestArgs("push", insecure), tag)
	return dockercli.RunDockerSimpleCommandWithVerbosity(pushArgs, verbosity, stdout, stderr)
}

func tagFingerprintAfterBuild(buildInput imagebuild.DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	if buildInput.Fingerprint == "" {
		return nil
	}
	sourceTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	target := dockerspec.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	if sourceTag == target {
		return nil
	}
	return dockercli.RunDockerTag(sourceTag, target, stdout, stderr)
}

// tagStableBaseVersionAfterBuild tags a snapshot base locally per arch so wrapper builds
// can resolve it; no-op for release builds.
func tagStableBaseVersionAfterBuild(buildInput imagebuild.DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	target := dockerspec.StableBaseVersionTag(buildInput.Image)
	if target == "" {
		return nil
	}
	sourceTag := dockerspec.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	if archTarget := dockerspec.PlatformSuffixedTag(target, platform); archTarget != sourceTag {
		if err := dockercli.RunDockerTag(sourceTag, archTarget, stdout, stderr); err != nil {
			return err
		}
	}
	if sourceTag == target {
		return nil
	}
	return dockercli.RunDockerTag(sourceTag, target, stdout, stderr)
}

// RunDockerBuildOnce captures --progress=plain output below debug verbosity and flushes
// it on failure; it returns the captured output for step timing.
func RunDockerBuildOnce(args []string, dir, authContextTag string, push bool, verbosity int, stdout, stderr io.Writer) (string, error) {
	cmd := core.Command("docker", args...)
	cmd.Dir = dir
	capture := &core.CommandOutputCapture{}
	if verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(stdout, &capture.Stdout)
		cmd.Stderr = core.TeeWriter(stderr, &capture.Stderr)
	} else {
		cmd.Stdout = &capture.Stdout
		cmd.Stderr = &capture.Stderr
	}
	err := cmd.Run()
	message := capture.Combined()
	if err == nil {
		return message, nil
	}

	if verbosity < core.VerbosityDebug && stderr != nil {
		_, _ = io.WriteString(stderr, message)
	}
	if push && dockerregistry.IsDockerPushAuthorizationError(message) {
		return message, model.DockerRegistryAuthError{
			Tag:      authContextTag,
			Registry: registrypkg.DockerRegistryFromImageTag(authContextTag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}
	if diagnosis, ok := diagnose.DockerBuildResourceExhaustionDiagnosis(message, err); ok {
		return message, diagnose.DockerBuildResourceExhaustionError{Diagnosis: diagnosis, Err: err}
	}
	// The step's last words are all the durable timing record will have.
	reason := diagnose.DockerBuildFailureReason(message)
	if diagnosis, ok := mtu.DockerBuildNetworkDiagnosis(message); ok {
		reason = diagnose.JoinFailureReason(reason, diagnosis)
	}
	if reason != "" {
		return message, diagnose.DockerBuildStepError{Reason: reason, Err: err}
	}
	return message, err
}
