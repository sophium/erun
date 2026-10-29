package releasespec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build/linuxpkg"
	"github.com/sophium/erun/erun-common/primitive/shared/buildversion"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"gopkg.in/yaml.v3"
)

func discoverReleaseArtifacts(ctx core.Context, inputs releaseInputs) (releaseArtifacts, error) {
	charts, fileUpdates, err := discoverReleaseCharts(inputs.ReleaseRoot, inputs.Version)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts := releaseArtifacts{Charts: charts, FileUpdates: fileUpdates}
	if err := discoverStableReleaseArtifacts(ctx, inputs, &artifacts); err != nil {
		return releaseArtifacts{}, err
	}
	images, err := discoverReleaseDockerImages(ctx, inputs.ProjectRoot, inputs.ReleaseRoot, inputs.VersionFilePath, inputs.Version)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts.Images = images
	linuxPackages, err := DiscoverReleaseLinuxPackages(inputs.ProjectRoot, inputs.ReleaseRoot)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts.LinuxPackages = linuxPackages
	return artifacts, nil
}

func discoverStableReleaseArtifacts(ctx core.Context, inputs releaseInputs, artifacts *releaseArtifacts) error {
	if inputs.Mode != model.ReleaseModeStable {
		return nil
	}
	if err := validateStableScoopManifest(ctx, inputs.ProjectRoot); err != nil {
		return err
	}
	packagingUpdates, syncSpec, err := discoverStableReleasePackaging(inputs.ProjectRoot, inputs.Version)
	if err != nil {
		return err
	}
	artifacts.FileUpdates = append(artifacts.FileUpdates, packagingUpdates...)
	artifacts.PackagingSync = syncSpec
	return nil
}

// releaseChartSearchRoot scopes chart discovery to the declared devops module, if any, so a
// legacy module's charts are not published too.
func releaseChartSearchRoot(projectRoot string) (string, error) {
	k8sDir, ok, err := dockerbuild.ConfiguredK8sDir(projectRoot)
	if err != nil {
		return "", err
	}
	if ok {
		return k8sDir, nil
	}
	dockerDir, ok, err := dockerbuild.ConfiguredDockerDir(projectRoot)
	if err != nil {
		return "", err
	}
	if ok {
		return filepath.Dir(dockerDir), nil
	}
	return projectRoot, nil
}

func discoverReleaseCharts(projectRoot, version string) ([]model.ReleaseChartSpec, []model.ReleaseFileUpdate, error) {
	searchRoot, err := releaseChartSearchRoot(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	chartPaths, err := findReleaseChartPaths(projectRoot, searchRoot)
	if err != nil {
		return nil, nil, err
	}

	charts := make([]model.ReleaseChartSpec, 0, len(chartPaths))
	updates := make([]model.ReleaseFileUpdate, 0, len(chartPaths))
	for _, chartPath := range chartPaths {
		content, changed, chartSpec, err := updateHelmChartReleaseVersion(filepath.Join(chartPath, "Chart.yaml"), version)
		if err != nil {
			return nil, nil, err
		}
		charts = append(charts, chartSpec)
		if changed {
			updates = append(updates, model.ReleaseFileUpdate{
				Path:    filepath.Join(chartPath, "Chart.yaml"),
				Content: content,
			})
		}
	}
	return charts, updates, nil
}

// releaseDockerDir honours paths.docker like the build does, else <release root>/docker.
func releaseDockerDir(projectRoot, releaseRoot string) (string, error) {
	configured, ok, err := dockerbuild.ConfiguredDockerDir(projectRoot)
	if err != nil {
		return "", err
	}
	if ok {
		return configured, nil
	}
	return filepath.Join(releaseRoot, "docker"), nil
}

// sameVersionFile reports whether two VERSION paths are the same file; a symlinked one belongs to the release.
func sameVersionFile(a, b string) bool {
	return versionFileIdentity(a) == versionFileIdentity(b)
}

func versionFileIdentity(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

func discoverReleaseDockerImages(ctx core.Context, projectRoot, releaseRoot, versionFilePath, version string) ([]model.ReleaseDockerImageSpec, error) {
	dockerDir, err := releaseDockerDir(projectRoot, releaseRoot)
	if err != nil {
		return nil, err
	}
	buildContexts, err := dockerspec.DockerBuildContextsUnderDir(dockerDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(buildContexts) == 0 {
		return nil, nil
	}

	registry, _, err := dockerbuild.ResolveDockerBuildRegistryForEnvironment(ctx, projectRoot, "")
	if err != nil {
		return nil, err
	}

	images := make([]model.ReleaseDockerImageSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		image, included, err := releaseDockerImageForContext(buildContext, releaseRoot, versionFilePath, version, registry)
		if err != nil {
			return nil, err
		}
		if included {
			images = append(images, image)
		}
	}
	return images, nil
}

// releaseDockerImageForContext excludes a context versioned by a different VERSION file.
func releaseDockerImageForContext(buildContext model.DockerBuildContext, releaseRoot, versionFilePath, version, registry string) (model.ReleaseDockerImageSpec, bool, error) {
	_, _, candidateVersionFilePath, err := buildversion.ResolveDockerBuildVersion(buildContext.Dir, releaseRoot, "")
	if err != nil {
		return model.ReleaseDockerImageSpec{}, false, err
	}
	if !sameVersionFile(candidateVersionFilePath, versionFilePath) {
		return model.ReleaseDockerImageSpec{}, false, nil
	}

	imageName := strings.TrimSpace(filepath.Base(buildContext.Dir))
	tag := imageName + ":" + version
	if strings.TrimSpace(registry) != "" {
		tag = strings.TrimRight(registry, "/") + "/" + tag
	}
	contextDir, err := dockerbuild.ResolveDockerBuildContextDirForProject(buildContext.Dir, releaseRoot, "")
	if err != nil {
		return model.ReleaseDockerImageSpec{}, false, err
	}
	return model.ReleaseDockerImageSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		ImageName:      imageName,
		Registry:       registry,
		Tag:            tag,
		Version:        version,
	}, true, nil
}

// DiscoverReleaseLinuxPackages resolves <release root>/linux components; each must declare a dist stage.
func DiscoverReleaseLinuxPackages(projectRoot, releaseRoot string) ([]model.LinuxPackageSpec, error) {
	contexts, err := linuxpkg.ResolveLinuxPackageContextsAtDir(filepath.Join(releaseRoot, "linux"))
	if err != nil {
		if errors.Is(err, dockerspec.ErrLinuxPackageBuildNotFound) {
			return nil, nil
		}
		return nil, err
	}
	packages := dockerspec.NewLinuxPackageSpecs(projectRoot, contexts)
	if err := linuxpkg.CheckLinuxPackagesReleasable(packages); err != nil {
		return nil, err
	}
	return packages, nil
}

func linuxPackageDirForRelease(inputs releaseInputs, packages []model.LinuxPackageSpec) string {
	if len(packages) == 0 {
		return ""
	}
	return linuxpkg.ReleaseLinuxPackageDir(inputs.ProjectRoot, inputs.Version)
}

func updateHelmChartReleaseVersion(chartFilePath, version string) (string, bool, model.ReleaseChartSpec, error) {
	data, err := os.ReadFile(chartFilePath)
	if err != nil {
		return "", false, model.ReleaseChartSpec{}, err
	}

	var chart map[string]interface{}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return "", false, model.ReleaseChartSpec{}, err
	}
	if chart == nil {
		return "", false, model.ReleaseChartSpec{}, errors.New("chart.yaml is empty")
	}

	changed := false
	if strings.TrimSpace(fmt.Sprint(chart["version"])) != version {
		chart["version"] = version
		changed = true
	}
	if strings.TrimSpace(fmt.Sprint(chart["appVersion"])) != version {
		chart["appVersion"] = version
		changed = true
	}

	updated, err := yaml.Marshal(chart)
	if err != nil {
		return "", false, model.ReleaseChartSpec{}, err
	}

	return string(updated), changed, model.ReleaseChartSpec{
		ChartPath:  filepath.Dir(chartFilePath),
		Version:    version,
		AppVersion: version,
	}, nil
}

func findReleaseChartPaths(projectRoot, searchRoot string) ([]string, error) {
	ignored, err := loadReleaseDiscoveryIgnoreSet(projectRoot)
	if err != nil {
		return nil, err
	}
	matches := make([]string, 0, 4)
	err = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip := releaseDiscoverySkipDir(projectRoot, path, d, ignored); skip {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "Chart.yaml" {
			return nil
		}
		matches = append(matches, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}
