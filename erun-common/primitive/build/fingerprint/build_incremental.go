package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/primitive/build/ignore"
	"github.com/sophium/erun/erun-common/primitive/build/order"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func DockerfileCopySources(dockerfilePath, contextDir string) ([]string, error) {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil, err
	}
	sources := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(rel string) {
		rel = filepath.Clean(rel)
		if rel == "" || rel == "." {
			return
		}
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		sources = append(sources, rel)
	}
	rel, err := DockerfileRelativePath(dockerfilePath, contextDir)
	if err == nil && rel != "" {
		add(rel)
	}
	for _, copySources := range ParseDockerfileCopyInstructions(string(data)) {
		for _, src := range copySources {
			add(src)
		}
	}
	return sources, nil
}

func DockerfileRelativePath(dockerfilePath, contextDir string) (string, error) {
	if strings.TrimSpace(contextDir) == "" {
		return "", errors.New("empty context dir")
	}
	rel, err := filepath.Rel(contextDir, dockerfilePath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", errors.New("dockerfile is outside context dir")
	}
	return rel, nil
}

var dockerfileCopyPattern = regexp.MustCompile(`(?im)^\s*COPY\s+(.+?)\s*$`)

func ParseDockerfileCopyInstructions(dockerfile string) [][]string {
	matches := dockerfileCopyPattern.FindAllStringSubmatch(dockerfile, -1)
	results := make([][]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		args := strings.Fields(match[1])
		if len(args) < 2 {
			continue
		}
		filtered, fromStage := filterDockerfileCopyArgs(args)
		if fromStage {
			continue
		}
		if len(filtered) < 2 {
			continue
		}
		sources := filtered[:len(filtered)-1]
		results = append(results, sources)
	}
	return results
}

// filterDockerfileCopyArgs reports fromStage for COPY --from, whose sources are not host paths.
func filterDockerfileCopyArgs(args []string) (filtered []string, fromStage bool) {
	filtered = make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "--from=") {
			fromStage = true
			continue
		}
		if strings.HasPrefix(arg, "--chown=") || strings.HasPrefix(arg, "--chmod=") || strings.HasPrefix(arg, "--link") || strings.HasPrefix(arg, "--parents") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, fromStage
}

// computeBuildFingerprint derives a build's content identity from its inputs only, never
// the pod it runs in, so a pod roll does not force a cold rebuild.
func computeBuildFingerprint(buildInput imagebuild.DockerBuildSpec) (string, error) {
	contextDir := strings.TrimSpace(buildInput.ContextDir)
	if contextDir == "" {
		return "", errors.New("empty context dir")
	}
	dockerfilePath := strings.TrimSpace(buildInput.DockerfilePath)
	if dockerfilePath == "" {
		return "", errors.New("empty dockerfile path")
	}
	sources, err := DockerfileCopySources(dockerfilePath, contextDir)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	if err := HashContextSourcesInto(hasher, contextDir, sources); err != nil {
		return "", err
	}
	if err := hashComponentChartInto(hasher, buildInput); err != nil {
		return "", err
	}
	if err := hashBuildVersionInto(hasher, buildInput); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	return digest[:16], nil
}

func HashContextSourcesInto(hasher io.Writer, contextDir string, sources []string) error {
	ignored, err := ignore.LoadContextIgnoreSet(contextDir, sources)
	if err != nil {
		return err
	}
	files, err := CollectFingerprintFiles(contextDir, sources, ignored)
	if err != nil {
		return err
	}
	return hashFingerprintFiles(hasher, contextDir, files)
}

// NUL separators prevent file-list collisions; slash-normalized paths keep it host-stable.
func hashFingerprintFiles(hasher io.Writer, contextDir string, files []string) error {
	for _, path := range files {
		rel, err := filepath.Rel(contextDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, err := hasher.Write([]byte(rel)); err != nil {
			return err
		}
		if _, err := hasher.Write([]byte{'\n'}); err != nil {
			return err
		}
		if err := hashFileInto(hasher, path); err != nil {
			return err
		}
		if _, err := hasher.Write([]byte{0}); err != nil {
			return err
		}
	}
	return nil
}

// hashBuildVersionInto folds ERUN_VERSION into builds that consume it, so a version bump
// does not promote an image reporting the old version.
func hashBuildVersionInto(w io.Writer, buildInput imagebuild.DockerBuildSpec) error {
	if !order.DockerfileConsumesVersion(buildInput.DockerfilePath) {
		return nil
	}
	// Unsuffixed, so both per-arch fingerprints move together.
	version := dockerspec.DockerBuildArgVersion(buildInput)
	if version == "" {
		return nil
	}
	if _, err := io.WriteString(w, "build-arg/ERUN_VERSION="+version+"\n"); err != nil {
		return err
	}
	_, err := w.Write([]byte{0})
	return err
}

// hashComponentChartInto folds the component's chart in, so a chart-only edit is not dropped by promotion.
func hashComponentChartInto(w io.Writer, buildInput imagebuild.DockerBuildSpec) error {
	// A version-pinned base's chart is bumped every release; folding it in would churn its fingerprint.
	if buildInput.Image.VersionFromBuildDir {
		return nil
	}
	chartDir := componentChartDirForBuild(buildInput)
	if chartDir == "" {
		return nil
	}
	return filepath.WalkDir(chartDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(chartDir, path)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, "chart/"+filepath.ToSlash(rel)+"\n"); err != nil {
			return err
		}
		if err := hashFileInto(w, path); err != nil {
			return err
		}
		_, err = w.Write([]byte{0})
		return err
	})
}

// componentChartDirForBuild returns the component's chart dir, or "" when it has none.
func componentChartDirForBuild(buildInput imagebuild.DockerBuildSpec) string {
	dockerfilePath := strings.TrimSpace(buildInput.DockerfilePath)
	if dockerfilePath == "" {
		return ""
	}
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(strings.TrimSpace(buildInput.ContextDir), dockerfilePath)
	}
	dockerDir := filepath.Dir(dockerfilePath)
	if filepath.Base(filepath.Dir(dockerDir)) != "docker" {
		return ""
	}
	chartDir := componentChartDirCandidate(buildInput.Image.ProjectRoot, dockerDir)
	if _, err := os.Stat(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		return ""
	}
	return chartDir
}

// componentChartDirCandidate resolves the chart dir as deploy does, so the hashed chart is
// the one that ships; it never fails the build.
func componentChartDirCandidate(projectRoot, dockerDir string) string {
	component := filepath.Base(dockerDir)
	if k8sDir, ok, err := dockerbuild.ResolveCurrentDevopsK8sDir(projectroot.FindProjectRoot, projectRoot, projectRoot); err == nil && ok {
		return filepath.Join(k8sDir, component)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(dockerDir)), "k8s", component)
}

func CollectFingerprintFiles(contextDir string, sources []string, ignored *ignore.IgnoreSet) ([]string, error) {
	files := make([]string, 0, 64)
	seen := make(map[string]struct{}, 64)
	add := func(p string) {
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		files = append(files, p)
	}
	for _, src := range sources {
		full := filepath.Join(contextDir, src)
		info, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			if err := collectFingerprintFile(contextDir, full, ignored, add); err != nil {
				return nil, err
			}
			continue
		}
		if err := collectFingerprintDir(contextDir, full, ignored, add); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func collectFingerprintFile(contextDir, full string, ignored *ignore.IgnoreSet, add func(string)) error {
	rel, err := filepath.Rel(contextDir, full)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if !ignored.Matches(rel, false) {
		add(full)
	}
	return nil
}

func collectFingerprintDir(contextDir, full string, ignored *ignore.IgnoreSet, add func(string)) error {
	return filepath.WalkDir(full, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(contextDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && ignored.Matches(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored.Matches(rel, false) {
			return nil
		}
		add(path)
		return nil
	})
}

func hashFileInto(w io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.Copy(w, file)
	return err
}

// LocalDockerImageInspector reports whether an image tag exists in the local daemon.
type LocalDockerImageInspector func(tag string) (bool, error)

// ApplyIncrementalToBuildExecution applies incremental promotion to an execution's docker builds.
func ApplyIncrementalToBuildExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, noIncremental bool) (dockerspec.BuildExecutionSpec, error) {
	updated, err := ApplyIncrementalToDockerBuilds(ctx, execution.DockerBuilds, noIncremental)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	execution.DockerBuilds = updated
	return execution, nil
}

// ApplyIncrementalToDockerBuilds is the single incremental-promotion entry point for every
// command. In dry-run, configured fingerprint tags are treated as present.
func ApplyIncrementalToDockerBuilds(ctx core.Context, builds []imagebuild.DockerBuildSpec, noIncremental bool) ([]imagebuild.DockerBuildSpec, error) {
	if len(builds) == 0 {
		return builds, nil
	}
	if noIncremental {
		return builds, nil
	}
	materialized, err := materializeConfiguredFingerprints(ctx, builds)
	if err != nil {
		return nil, err
	}
	var inspect LocalDockerImageInspector
	if len(materialized) > 0 {
		inspect = func(tag string) (bool, error) {
			if _, ok := materialized[tag]; ok {
				return true, nil
			}
			return dockerregistry.DockerImageExists(tag)
		}
	}
	return applyIncrementalPromotion(builds, inspect)
}

// applyIncrementalPromotion promotes builds whose fp-tagged image exists locally; a rebuilt
// local base forces its dependents to rebuild.
func applyIncrementalPromotion(builds []imagebuild.DockerBuildSpec, inspect LocalDockerImageInspector) ([]imagebuild.DockerBuildSpec, error) {
	if len(builds) == 0 {
		return builds, nil
	}
	if inspect == nil {
		inspect = dockerregistry.DockerImageExists
	}
	out := make([]imagebuild.DockerBuildSpec, len(builds))
	copy(out, builds)
	rebuildSet := make(map[string]struct{}, len(out))
	for i := range out {
		fingerprint, err := computeBuildFingerprint(out[i])
		if err != nil {
			rebuildSet[strings.TrimSpace(out[i].Image.Tag)] = struct{}{}
			continue
		}
		out[i].Fingerprint = fingerprint
		missing, err := inspectFingerprintTags(out[i], inspect)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			out[i].MissingFingerprintPlatforms = missing
			rebuildSet[strings.TrimSpace(out[i].Image.Tag)] = struct{}{}
			continue
		}
		out[i].Promote = true
	}
	cascadeRebuildsThroughLocalDeps(out, rebuildSet)
	for i := range out {
		if _, ok := rebuildSet[strings.TrimSpace(out[i].Image.Tag)]; ok {
			out[i].Promote = false
		}
	}
	return out, nil
}

func inspectFingerprintTags(build imagebuild.DockerBuildSpec, inspect LocalDockerImageInspector) ([]string, error) {
	if build.Fingerprint == "" {
		return nil, nil
	}
	var missing []string
	for _, platform := range build.Platforms {
		ok, err := inspect(dockerspec.FingerprintTag(build.Image, build.Fingerprint, platform))
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, platform)
		}
	}
	return missing, nil
}

func cascadeRebuildsThroughLocalDeps(builds []imagebuild.DockerBuildSpec, rebuildSet map[string]struct{}) {
	if len(rebuildSet) == 0 {
		return
	}
	buildsByTag := order.DockerBuildsByTag(builds)
	changed := true
	for changed {
		changed = false
		for i := range builds {
			tag := strings.TrimSpace(builds[i].Image.Tag)
			if _, ok := rebuildSet[tag]; ok {
				continue
			}
			deps := order.DockerfileLocalBaseImageTags(builds[i].DockerfilePath, buildsByTag)
			for _, dep := range deps {
				dep = strings.TrimSpace(dep)
				if _, ok := rebuildSet[dep]; !ok {
					continue
				}
				rebuildSet[tag] = struct{}{}
				if builds[i].CascadeRebuildFromTag == "" {
					builds[i].CascadeRebuildFromTag = dep
				}
				changed = true
				break
			}
		}
	}
}
