package build

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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
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
	rel, err := dockerfileRelativePath(dockerfilePath, contextDir)
	if err == nil && rel != "" {
		add(rel)
	}
	for _, copySources := range parseDockerfileCopyInstructions(string(data)) {
		for _, src := range copySources {
			add(src)
		}
	}
	return sources, nil
}

func dockerfileRelativePath(dockerfilePath, contextDir string) (string, error) {
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

func parseDockerfileCopyInstructions(dockerfile string) [][]string {
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

// filterDockerfileCopyArgs reports fromStage when a COPY --from=<stage> is seen:
// such a COPY references a build stage, not a host path, so its sources are not
// fingerprint inputs and the caller skips the instruction entirely.
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

// computeBuildFingerprint derives a content identity for a build. Ignored files
// (.dockerignore/.gitignore) are excluded so generated artifacts and untracked
// state do not churn the result, and the digest is truncated to 16 chars to fit
// in a Docker tag.
//
// This identity — and the fp-<fingerprint> tag a promotion resolves — is
// deliberately a function of the build's *inputs* alone: the context the
// Dockerfile copies, its chart, and the version it consumes. It must never
// become a function of the container it is computed in, because the container
// is the one thing an environment does not keep: a deploy or a resize replaces
// the pod (new hostname, new cgroup namespace, new per-pod paths) while the
// repository, the local Docker store and the cache volume all survive, so an
// input taken from the pod would move the identity on every roll and make a
// warm environment rebuild from cold. The one pod-scoped value a build carries
// is CgroupParent, derived from the pod's own hostname; it is a runtime
// placement for the RUN containers rather than an input to the image they
// produce, so it stays out of this set (see buildContainerCPUCapCgroupParent,
// which also records what a roll pays for it in BuildKit's own layer cache).
// TestBuildFingerprintSurvivesAPodRoll
// pins that, including for a content change that must still move the identity.
func computeBuildFingerprint(buildInput shared.DockerBuildSpec) (string, error) {
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
	if err := hashContextSourcesInto(hasher, contextDir, sources); err != nil {
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

// hashContextSourcesInto hashes every file the sources name in the context,
// less what the context's ignore files exclude: the same input set for an
// image and for a linux package component.
func hashContextSourcesInto(hasher io.Writer, contextDir string, sources []string) error {
	ignored, err := LoadContextIgnoreSet(contextDir, sources)
	if err != nil {
		return err
	}
	files, err := CollectFingerprintFiles(contextDir, sources, ignored)
	if err != nil {
		return err
	}
	return hashFingerprintFiles(hasher, contextDir, files)
}

// hashFingerprintFiles separates entries with a NUL byte so distinct file lists
// can never collide into the same digest, and slash-normalizes paths so the
// result is stable across host filesystems.
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

// hashBuildVersionInto folds the ERUN_VERSION build arg into the fingerprint of
// every build whose Dockerfile consumes it. The version is a build input that
// lives in no file in the context, so hashing only files answered "is this the
// same source?" where a versioned artifact needs "is this the same artifact?":
// a release whose only diff was VERSION promoted the previous release's image,
// and the binary inside kept reporting the version before the tag it shipped
// under. A build that never references the version keeps a version-independent
// identity, so the cache still spans releases for everything that does not bake
// one in.
func hashBuildVersionInto(w io.Writer, buildInput shared.DockerBuildSpec) error {
	if !dockerfileConsumesVersion(buildInput.DockerfilePath) {
		return nil
	}
	// The unsuffixed value, so both per-arch fingerprints stay identical and one
	// version change still invalidates them together.
	version := shared.DockerBuildArgVersion(buildInput)
	if version == "" {
		return nil
	}
	if _, err := io.WriteString(w, "build-arg/ERUN_VERSION="+version+"\n"); err != nil {
		return err
	}
	_, err := w.Write([]byte{0})
	return err
}

// hashComponentChartInto folds the component's Helm chart into the build
// fingerprint. The chart ships at the same version as the image but lives
// outside the image build context, so without this a chart-only edit would
// leave the fingerprint unchanged, the image would promote from cache, and the
// chart change would be silently dropped. Hashing it keeps image and chart one
// versioned contract. A component with no chart contributes nothing.
func hashComponentChartInto(w io.Writer, buildInput shared.DockerBuildSpec) error {
	// A version-pinned base image takes its identity from its upstream pin, not
	// the release, but its chart is bumped to the release version every cycle.
	// Folding that chart in would churn a stable image's fingerprint every
	// release and force a needless rebuild, so skip it; the chart still publishes
	// at the release version through push regardless.
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

// componentChartDirForBuild derives the component's chart dir from the repo
// convention that a charted component's Dockerfile at <module>/docker/<component>
// has its chart at the sibling <module>/k8s/<component>. Returns "" when the
// Dockerfile is outside that layout or the component has no chart.
func componentChartDirForBuild(buildInput shared.DockerBuildSpec) string {
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

// componentChartDirCandidate resolves the component's chart dir the same way
// deploy resolves it — the paths.k8s override, else the -devops convention — so
// the chart folded into the image fingerprint is always the chart that ships,
// preserving the image+chart one-version contract even when paths.docker
// relocates the build root away from the k8s tree. It falls back to the
// sibling-of-docker convention only for layouts the deploy resolver cannot
// anchor (no git-detected tenant, or an ambiguous set of modules), and never
// fails the build on a resolver error.
func componentChartDirCandidate(projectRoot, dockerDir string) string {
	component := filepath.Base(dockerDir)
	if k8sDir, ok, err := shared.ResolveCurrentDevopsK8sDir(config.FindProjectRoot, projectRoot, projectRoot); err == nil && ok {
		return filepath.Join(k8sDir, component)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(dockerDir)), "k8s", component)
}

func CollectFingerprintFiles(contextDir string, sources []string, ignored *shared.IgnoreSet) ([]string, error) {
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

func collectFingerprintFile(contextDir, full string, ignored *shared.IgnoreSet, add func(string)) error {
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

func collectFingerprintDir(contextDir, full string, ignored *shared.IgnoreSet, add func(string)) error {
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

// LoadContextIgnoreSet builds the ignore matcher for fingerprint computation.
// Patterns are appended in load order (root files, then nested .gitignores) so
// a later negation can override an earlier exclusion.
func LoadContextIgnoreSet(contextDir string, sources []string) (*shared.IgnoreSet, error) {
	combined := &shared.IgnoreSet{}
	for _, name := range []string{".dockerignore", ".gitignore"} {
		set, err := shared.LoadIgnoreFile(filepath.Join(contextDir, name), "")
		if err != nil {
			return nil, err
		}
		combined.Patterns = append(combined.Patterns, set.Patterns...)
	}
	if err := shared.LoadNestedGitignores(contextDir, sources, combined); err != nil {
		return nil, err
	}
	return combined, nil
}

// LocalDockerImageInspector reports whether an image tag exists in the local
// Docker daemon, used to detect fp-tagged images eligible for incremental
// promotion.
type LocalDockerImageInspector func(tag string) (bool, error)

// ApplyIncrementalToBuildExecution applies fingerprint-based incremental
// promotion to the execution's docker builds, returning a copy (unchanged when
// noIncremental is true).
func ApplyIncrementalToBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, noIncremental bool) (shared.BuildExecutionSpec, error) {
	updated, err := ApplyIncrementalToDockerBuilds(ctx, execution.DockerBuilds, noIncremental)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}
	execution.DockerBuilds = updated
	return execution, nil
}

// ApplyIncrementalToDockerBuilds applies fingerprint-based incremental
// promotion to a slice of docker builds. This is the single entry point every
// command uses, so deploy, push, and runtime deploy share the same skip logic as
// erun build. Images listed in the project's docker.fingerprints config are
// first pulled and tagged locally under their configured fingerprint so a
// matching build promotes instead of rebuilding; in dry-run those would-be tags
// are treated as present so the trace still reflects the promote path.
//
// A tested component is promotable like any other: an fp tag is written only
// after a successful docker build, which ran the Dockerfile's test stage, so
// promoting from it reuses that component's last successful result.
func ApplyIncrementalToDockerBuilds(ctx core.Context, builds []shared.DockerBuildSpec, noIncremental bool) ([]shared.DockerBuildSpec, error) {
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
			return shared.DockerImageExists(tag)
		}
	}
	return applyIncrementalPromotion(builds, inspect)
}

// applyIncrementalPromotion marks each build whose fp-tagged image already
// exists locally for promotion (re-tag + push) instead of rebuild. Cascade: a
// rebuilt local-base image forces every dependent that FROMs it to rebuild too,
// even when the dependent's own fingerprint matches.
func applyIncrementalPromotion(builds []shared.DockerBuildSpec, inspect LocalDockerImageInspector) ([]shared.DockerBuildSpec, error) {
	if len(builds) == 0 {
		return builds, nil
	}
	if inspect == nil {
		inspect = shared.DockerImageExists
	}
	out := make([]shared.DockerBuildSpec, len(builds))
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

func inspectFingerprintTags(build shared.DockerBuildSpec, inspect LocalDockerImageInspector) ([]string, error) {
	if build.Fingerprint == "" {
		return nil, nil
	}
	var missing []string
	for _, platform := range build.Platforms {
		ok, err := inspect(shared.FingerprintTag(build.Image, build.Fingerprint, platform))
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, platform)
		}
	}
	return missing, nil
}

func cascadeRebuildsThroughLocalDeps(builds []shared.DockerBuildSpec, rebuildSet map[string]struct{}) {
	if len(rebuildSet) == 0 {
		return
	}
	buildsByTag := dockerBuildsByTag(builds)
	changed := true
	for changed {
		changed = false
		for i := range builds {
			tag := strings.TrimSpace(builds[i].Image.Tag)
			if _, ok := rebuildSet[tag]; ok {
				continue
			}
			deps := dockerfileLocalBaseImageTags(builds[i].DockerfilePath, buildsByTag)
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
