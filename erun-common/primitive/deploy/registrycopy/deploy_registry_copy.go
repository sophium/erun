package registrycopy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartfiles"
)

// registryCopy mirrors one image FROM -> TO, preserving its multi-arch manifest
// list.
type registryCopy struct {
	From  string
	To    string
	Image string // "name:version"
}

func (c registryCopy) command() helm.CommandSpec {
	return helm.CommandSpec{
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

// RunDeployRegistryCopies mirrors images FROM -> each TO registry, tracing every
// copy even under --dry-run.
func RunDeployRegistryCopies(ctx core.Context, execution seam.DeploySpec) error {
	copies, err := ResolveDeployRegistryCopies(execution)
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
		runner := core.Command(cmd.Name, cmd.Args...)
		runner.Stdout = ctx.Stdout
		runner.Stderr = ctx.Stderr
		if err := runner.Run(); err != nil {
			return fmt.Errorf("copy image %s from %s to %s: %w", copy.Image, copy.From, copy.To, err)
		}
	}
	return nil
}

func ResolveDeployRegistryCopies(execution seam.DeploySpec) ([]registryCopy, error) {
	list, err := config.DeployTargetContainerRegistries(execution.Target)
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

func deployCopyImages(execution seam.DeploySpec) []string {
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
	if execution.Deploy.ReleaseName == config.RuntimeReleaseName(execution.Target.Tenant) {
		name, version := runtimeCopyImage(execution)
		add(name, version)
	}
	chartImages, err := chartfiles.FindDockerImagesInChart(execution.DeployContext.ChartPath, execution.Deploy.Version)
	if err == nil {
		for _, image := range chartImages {
			add(imageNameVersion(image))
		}
	}
	return images
}

func runtimeCopyImage(execution seam.DeploySpec) (string, string) {
	if override := strings.TrimSpace(execution.Deploy.ImageOverrides[config.DevopsComponentName]); override != "" {
		return imageNameVersion(override)
	}
	return config.DevopsComponentName, execution.Deploy.Version
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
