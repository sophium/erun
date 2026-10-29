package dockerspec

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// CheckImageDockerfile refuses a Dockerfile that is not an image component's:
// an e2e suite, a linux package, or one whose test stage nothing consumes.
func CheckImageDockerfile(dockerfilePath string) error {
	if err := CheckNotE2ESuiteDockerfile(dockerfilePath); err != nil {
		return err
	}
	if err := checkNotLinuxPackageDockerfile(dockerfilePath); err != nil {
		return err
	}
	return CheckDockerfileTestStageConsumed(dockerfilePath)
}

func DockerBuildTraceCommands(b imagebuild.DockerBuildSpec) []helm.CommandSpec {
	if b.Promote {
		return promoteTraceCommands(b)
	}
	return multiPlatformTraceCommands(b)
}

func multiPlatformTraceCommands(b imagebuild.DockerBuildSpec) []helm.CommandSpec {
	commands := make([]helm.CommandSpec, 0, len(b.Platforms)*4+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := StableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := PlatformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, helm.CommandSpec{
			Dir:  b.ContextDir,
			Name: "docker",
			Args: DockerBuildArgs(b, platform),
		})
		if b.Fingerprint != "" {
			commands = append(commands, dockerTagTraceCommand(b.ContextDir, platformTag, FingerprintTag(b.Image, b.Fingerprint, platform)))
		}
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, helm.CommandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: DockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, multiPlatformManifestTraceCommands(b.ContextDir, b.Image.Tag, perPlatformTags, b.Image.Insecure)...)
	return commands
}

// stableBaseVersionTraceCommands must mirror tagStableBaseVersionAfterBuild.
func stableBaseVersionTraceCommands(dir, platformTag, baseTag, platform string) []helm.CommandSpec {
	if baseTag == "" {
		return nil
	}
	commands := make([]helm.CommandSpec, 0, 2)
	if archTag := PlatformSuffixedTag(baseTag, platform); archTag != platformTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, archTag))
	}
	if platformTag != baseTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, baseTag))
	}
	return commands
}

func promoteTraceCommands(b imagebuild.DockerBuildSpec) []helm.CommandSpec {
	commands := make([]helm.CommandSpec, 0, len(b.Platforms)*3+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := StableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := PlatformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, dockerTagTraceCommand(b.ContextDir, FingerprintTag(b.Image, b.Fingerprint, platform), platformTag))
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, helm.CommandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: DockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, multiPlatformManifestTraceCommands(b.ContextDir, b.Image.Tag, perPlatformTags, b.Image.Insecure)...)
	return commands
}

// multiPlatformManifestTraceCommands must mirror assembleMultiPlatformManifest.
func multiPlatformManifestTraceCommands(dir, tag string, perPlatformTags []string, insecure bool) []helm.CommandSpec {
	createArgs := append(DockerManifestArgs("create", insecure), tag)
	createArgs = append(createArgs, perPlatformTags...)
	pushArgs := append(DockerManifestArgs("push", insecure), tag)
	return []helm.CommandSpec{
		{Dir: dir, Name: "docker", Args: []string{"manifest", "rm", tag}},
		{Dir: dir, Name: "docker", Args: createArgs},
		{Dir: dir, Name: "docker", Args: pushArgs},
	}
}

func dockerTagTraceCommand(dir, source, target string) helm.CommandSpec {
	return helm.CommandSpec{
		Dir:  dir,
		Name: "docker",
		Args: []string{"tag", source, target},
	}
}

func DockerPushCommand(p model.DockerPushSpec) helm.CommandSpec {
	return helm.CommandSpec{
		Dir:  p.Dir,
		Name: "docker",
		Args: DockerPushArgs(p.Image.Tag, p.Verbosity),
	}
}
