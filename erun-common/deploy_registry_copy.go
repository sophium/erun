package eruncommon

import (
	"fmt"
	"strings"
)

// registryCopy is one image mirrored from the FROM registry to a TO registry
// before the cluster pulls it from the DEPLOY registry. The copy is
// manifest-list-aware (docker buildx imagetools create) so the runtime image's
// multi-arch manifest survives the mirror.
type registryCopy struct {
	From  string
	To    string
	Image string // "name:version"
}

func (c registryCopy) command() commandSpec {
	return commandSpec{
		Name: "docker",
		Args: []string{
			"buildx", "imagetools", "create",
			"--tag", joinRegistryImage(c.To, c.Image),
			joinRegistryImage(c.From, c.Image),
		},
	}
}

func joinRegistryImage(registry, image string) string {
	return strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + strings.TrimSpace(image)
}

// runDeployRegistryCopies mirrors every image the deploy references from the
// FROM registry to each TO registry, when the environment's registry list
// marks both. Each copy is traced (so --dry-run is a complete audit) and the
// push is gated behind ctx.DryRun. A copy is skipped when no FROM/TO is marked
// — the common single-registry case emits nothing.
func runDeployRegistryCopies(ctx Context, execution DeploySpec) error {
	copies, err := resolveDeployRegistryCopies(execution)
	if err != nil {
		return err
	}
	for _, copy := range copies {
		cmd := copy.command()
		ctx.TraceCommand(cmd.Dir, cmd.Name, cmd.Args...)
	}
	if ctx.DryRun || len(copies) == 0 {
		return nil
	}
	for _, copy := range copies {
		ctx.Info("==> Copying " + copy.Image + " from " + copy.From + " to " + copy.To)
		cmd := copy.command()
		runner := Command(cmd.Name, cmd.Args...)
		runner.Stdout = ctx.Stdout
		runner.Stderr = ctx.Stderr
		if err := runner.Run(); err != nil {
			return fmt.Errorf("copy image %s from %s to %s: %w", copy.Image, copy.From, copy.To, err)
		}
	}
	return nil
}

// resolveDeployRegistryCopies builds the copy list for a resolved deploy: every
// image the chart pulls (the runtime devops image and any locally-built
// component images) mirrored from FROM to each TO. Returns nil when the
// environment marks no FROM/TO pair.
func resolveDeployRegistryCopies(execution DeploySpec) ([]registryCopy, error) {
	list, err := deployTargetContainerRegistries(execution.Target)
	if err != nil {
		return nil, err
	}
	from, hasFrom := list.FromRegistry()
	tos := list.ToRegistries()
	if !hasFrom || len(tos) == 0 {
		return nil, nil
	}
	images := deployCopyImages(execution)
	if len(images) == 0 {
		return nil, nil
	}
	copies := make([]registryCopy, 0, len(images)*len(tos))
	for _, image := range images {
		for _, to := range tos {
			if strings.EqualFold(strings.TrimSpace(to), strings.TrimSpace(from)) {
				continue
			}
			copies = append(copies, registryCopy{From: from, To: to, Image: image})
		}
	}
	return copies, nil
}

// deployCopyImages collects the "name:version" of every image the cluster
// pulls for this deploy: the runtime devops image (when the spec owns the
// runtime chart) plus each locally-built component image.
func deployCopyImages(execution DeploySpec) []string {
	seen := make(map[string]struct{})
	images := make([]string, 0, 2)
	add := func(name, version string) {
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if name == "" || version == "" {
			return
		}
		ref := name + ":" + version
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		images = append(images, ref)
	}
	if execution.Deploy.ReleaseName == RuntimeReleaseName(execution.Target.Tenant) {
		name, version := runtimeCopyImage(execution)
		add(name, version)
	}
	// The chart's own images at the deploy version (a pure deploy installs by
	// reference, so there are no local builds to enumerate; the chart names the
	// images the cluster pulls).
	chartImages, err := findDockerImagesInChart(execution.DeployContext.ChartPath, execution.Deploy.Version)
	if err == nil {
		for _, image := range chartImages {
			add(imageNameVersion(image))
		}
	}
	return images
}

// runtimeCopyImage returns the name and version of the runtime devops image the
// chart pulls — the imageOverrides entry when set, otherwise the default
// erun-devops image at the deploy version.
func runtimeCopyImage(execution DeploySpec) (string, string) {
	if override := strings.TrimSpace(execution.Deploy.ImageOverrides[DevopsComponentName]); override != "" {
		return imageNameVersion(override)
	}
	return DevopsComponentName, execution.Deploy.Version
}

// imageNameVersion splits a full image reference into its name and version,
// dropping the registry prefix. "ghcr.io/sophium/erun-devops:1.0.0" →
// ("erun-devops", "1.0.0").
func imageNameVersion(reference string) (string, string) {
	reference = strings.TrimSpace(reference)
	nameTag := reference
	if idx := strings.LastIndex(reference, "/"); idx >= 0 {
		nameTag = reference[idx+1:]
	}
	name, version, ok := strings.Cut(nameTag, ":")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(name), strings.TrimSpace(version)
}
