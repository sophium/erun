package eruncommon

import (
	"fmt"
	"strings"
)

// registryCopy is one image mirrored from the FROM registry to a TO registry
// before the cluster pulls it from the DEPLOY registry, preserving the image's
// multi-arch manifest list through the mirror.
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

// runDeployRegistryCopies mirrors the deploy's images from the FROM registry to
// each TO registry. Every copy is traced even under --dry-run so a dry-run is a
// complete audit of what a real deploy would push.
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
	// A pure deploy installs by reference and never builds, so the chart itself
	// names the images the cluster pulls.
	chartImages, err := findDockerImagesInChart(execution.DeployContext.ChartPath, execution.Deploy.Version)
	if err == nil {
		for _, image := range chartImages {
			add(imageNameVersion(image))
		}
	}
	return images
}

func runtimeCopyImage(execution DeploySpec) (string, string) {
	if override := strings.TrimSpace(execution.Deploy.ImageOverrides[DevopsComponentName]); override != "" {
		return imageNameVersion(override)
	}
	return DevopsComponentName, execution.Deploy.Version
}

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
