package eruncommon

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
)

func dockerfileCopySources(dockerfilePath, contextDir string) ([]string, error) {
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
func computeBuildFingerprint(buildInput DockerBuildSpec) (string, error) {
	contextDir := strings.TrimSpace(buildInput.ContextDir)
	if contextDir == "" {
		return "", errors.New("empty context dir")
	}
	dockerfilePath := strings.TrimSpace(buildInput.DockerfilePath)
	if dockerfilePath == "" {
		return "", errors.New("empty dockerfile path")
	}
	sources, err := dockerfileCopySources(dockerfilePath, contextDir)
	if err != nil {
		return "", err
	}
	ignored, err := loadContextIgnoreSet(contextDir, sources)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	files, err := collectFingerprintFiles(contextDir, sources, ignored)
	if err != nil {
		return "", err
	}
	if err := hashFingerprintFiles(hasher, contextDir, files); err != nil {
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
func hashBuildVersionInto(w io.Writer, buildInput DockerBuildSpec) error {
	if !dockerfileConsumesVersion(buildInput.DockerfilePath) {
		return nil
	}
	// The unsuffixed value, so both per-arch fingerprints stay identical and one
	// version change still invalidates them together.
	version := dockerBuildArgVersion(buildInput)
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
func hashComponentChartInto(w io.Writer, buildInput DockerBuildSpec) error {
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
func componentChartDirForBuild(buildInput DockerBuildSpec) string {
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
	if k8sDir, ok, err := resolveCurrentDevopsK8sDir(FindProjectRoot, projectRoot, projectRoot); err == nil && ok {
		return filepath.Join(k8sDir, component)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(dockerDir)), "k8s", component)
}

func collectFingerprintFiles(contextDir string, sources []string, ignored *ignoreSet) ([]string, error) {
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

func collectFingerprintFile(contextDir, full string, ignored *ignoreSet, add func(string)) error {
	rel, err := filepath.Rel(contextDir, full)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if !ignored.matches(rel, false) {
		add(full)
	}
	return nil
}

func collectFingerprintDir(contextDir, full string, ignored *ignoreSet, add func(string)) error {
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
			if rel != "." && ignored.matches(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored.matches(rel, false) {
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

type ignorePattern struct {
	raw      string
	anchored bool
	dirOnly  bool
	negate   bool
	// base scopes the pattern to the subtree of the nested .gitignore it came
	// from (empty for the root files), mirroring git, where a nested .gitignore
	// only governs files beneath it.
	base string
}

type ignoreSet struct {
	patterns []ignorePattern
}

// loadContextIgnoreSet builds the ignore matcher for fingerprint computation.
// Patterns are appended in load order (root files, then nested .gitignores) so
// a later negation can override an earlier exclusion.
func loadContextIgnoreSet(contextDir string, sources []string) (*ignoreSet, error) {
	combined := &ignoreSet{}
	for _, name := range []string{".dockerignore", ".gitignore"} {
		set, err := loadIgnoreFile(filepath.Join(contextDir, name), "")
		if err != nil {
			return nil, err
		}
		combined.patterns = append(combined.patterns, set.patterns...)
	}
	if err := loadNestedGitignores(contextDir, sources, combined); err != nil {
		return nil, err
	}
	return combined, nil
}

// loadNestedGitignores honours nested .gitignore files but intentionally not
// nested .dockerignore files: Docker reads only the root .dockerignore, so
// honouring nested ones would diverge from the real build context.
func loadNestedGitignores(contextDir string, sources []string, combined *ignoreSet) error {
	visited := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		full := filepath.Join(contextDir, src)
		info, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if !info.IsDir() {
			continue
		}
		if err := walkNestedGitignores(contextDir, full, visited, combined); err != nil {
			return err
		}
	}
	return nil
}

func walkNestedGitignores(contextDir, full string, visited map[string]struct{}, combined *ignoreSet) error {
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
			if _, seen := visited[rel]; seen {
				return filepath.SkipDir
			}
			visited[rel] = struct{}{}
			return nil
		}
		if d.Name() != ".gitignore" {
			return nil
		}
		base := filepath.ToSlash(filepath.Dir(rel))
		if base == "." || base == "" {
			return nil // root, already loaded
		}
		set, err := loadIgnoreFile(path, base)
		if err != nil {
			return err
		}
		combined.patterns = append(combined.patterns, set.patterns...)
		return nil
	})
}

func loadIgnoreFile(path, base string) (*ignoreSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ignoreSet{}, nil
		}
		return nil, err
	}
	return parseIgnoreData(data, base), nil
}

func parseIgnoreData(data []byte, base string) *ignoreSet {
	set := &ignoreSet{}
	for _, raw := range strings.Split(string(data), "\n") {
		pattern, ok := parseIgnoreLine(raw, base)
		if !ok {
			continue
		}
		set.patterns = append(set.patterns, pattern)
	}
	return set
}

func parseIgnoreLine(raw, base string) (ignorePattern, bool) {
	line := strings.TrimRight(raw, "\r")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = strings.TrimSpace(line[1:])
		if line == "" {
			return ignorePattern{}, false
		}
	}
	anchored := false
	if strings.HasPrefix(line, "/") {
		anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	dirOnly := false
	if strings.HasSuffix(line, "/") {
		dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	// gitignore: a pattern that contains a slash anywhere except the
	// trailing position is anchored to the file's directory. For our
	// purposes the file's directory is contextDir, the same as
	// leading-slash anchoring.
	if !anchored && strings.Contains(line, "/") {
		anchored = true
	}
	if line == "" {
		return ignorePattern{}, false
	}
	return ignorePattern{
		raw:      filepath.ToSlash(line),
		anchored: anchored,
		dirOnly:  dirOnly,
		negate:   negate,
		base:     base,
	}, true
}

func (s *ignoreSet) matches(rel string, isDir bool) bool {
	if s == nil || len(s.patterns) == 0 {
		return false
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	matched := false
	for _, p := range s.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if patternMatchesPath(p, rel) {
			matched = !p.negate
		}
	}
	return matched
}

func patternMatchesPath(p ignorePattern, rel string) bool {
	if p.raw == "" || p.raw == "." {
		return false
	}
	// Strip the nested .gitignore's base prefix so anchoring and globbing run
	// on a path relative to where the pattern was declared.
	if p.base != "" {
		if rel == p.base || !strings.HasPrefix(rel, p.base+"/") {
			return false
		}
		rel = strings.TrimPrefix(rel, p.base+"/")
	}
	if p.anchored {
		return anchoredPatternMatches(p.raw, rel)
	}
	// Non-anchored gitignore patterns match a basename anywhere in the tree.
	for _, part := range strings.Split(rel, "/") {
		if globMatch(p.raw, part) {
			return true
		}
	}
	return false
}

// anchoredPatternMatches also matches rel's ancestor prefixes so the walker can
// prune an ignored directory before descending into it.
func anchoredPatternMatches(raw, rel string) bool {
	if globMatch(raw, rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if globMatch(raw, prefix) {
			return true
		}
	}
	return false
}

// globMatch extends filepath.Match with `**`, which (unlike `*`) matches across
// path separators (e.g. `a/**/b` matches both `a/b` and `a/x/y/b`).
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := filepath.Match(pattern, name)
		return err == nil && ok
	}
	re, err := regexp.Compile(globToRegex(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		i = writeGlobToken(&b, pattern, i)
	}
	b.WriteString("$")
	return b.String()
}

func writeGlobToken(b *strings.Builder, pattern string, i int) int {
	switch {
	case i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*':
		b.WriteString(".*")
		i += 2
		// Collapse `**/` so `a/**/b` matches `a/b` as well as `a/x/b`.
		if i < len(pattern) && pattern[i] == '/' {
			i++
		}
		return i
	case pattern[i] == '*':
		b.WriteString("[^/]*")
		return i + 1
	case pattern[i] == '?':
		b.WriteString("[^/]")
		return i + 1
	case strings.ContainsRune(`.+()|^$\{}`, rune(pattern[i])):
		b.WriteByte('\\')
		b.WriteByte(pattern[i])
		return i + 1
	default:
		b.WriteByte(pattern[i])
		return i + 1
	}
}

// LocalDockerImageInspector reports whether an image tag exists in the local
// Docker daemon, used to detect fp-tagged images eligible for incremental
// promotion.
type LocalDockerImageInspector func(tag string) (bool, error)

// ApplyIncrementalToBuildExecution applies fingerprint-based incremental
// promotion to the execution's docker builds, returning a copy (unchanged when
// noIncremental is true).
func ApplyIncrementalToBuildExecution(ctx Context, execution BuildExecutionSpec, noIncremental bool) (BuildExecutionSpec, error) {
	updated, err := ApplyIncrementalToDockerBuilds(ctx, execution.dockerBuilds, noIncremental)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	execution.dockerBuilds = updated
	return execution, nil
}

// ApplyIncrementalToDockerBuilds applies fingerprint-based incremental
// promotion to a slice of docker builds (unchanged when noIncremental is true).
// This is the single entry point every command uses, so deploy, push, and
// runtime deploy share the same skip logic as erun build. Images listed in the
// project's docker.fingerprints config are first pulled and tagged locally under
// their configured fingerprint so a matching build promotes instead of
// rebuilding; in dry-run those would-be tags are treated as present so the trace
// still reflects the promote path.
func ApplyIncrementalToDockerBuilds(ctx Context, builds []DockerBuildSpec, noIncremental bool) ([]DockerBuildSpec, error) {
	if noIncremental || len(builds) == 0 {
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
			return DockerImageExists(tag)
		}
	}
	return applyIncrementalPromotion(builds, inspect)
}

// applyIncrementalPromotion marks each build whose fp-tagged image already
// exists locally for promotion (re-tag + push) instead of rebuild. Cascade: a
// rebuilt local-base image forces every dependent that FROMs it to rebuild too,
// even when the dependent's own fingerprint matches.
func applyIncrementalPromotion(builds []DockerBuildSpec, inspect LocalDockerImageInspector) ([]DockerBuildSpec, error) {
	if len(builds) == 0 {
		return builds, nil
	}
	if inspect == nil {
		inspect = DockerImageExists
	}
	out := make([]DockerBuildSpec, len(builds))
	copy(out, builds)
	rebuildSet := make(map[string]struct{}, len(out))
	for i := range out {
		fingerprint, err := computeBuildFingerprint(out[i])
		if err != nil {
			rebuildSet[strings.TrimSpace(out[i].Image.Tag)] = struct{}{}
			continue
		}
		out[i].Fingerprint = fingerprint
		if dockerfileHasGateTestStage(out[i].DockerfilePath) {
			// The fingerprint proves the inputs are unchanged, not that the gate
			// (make check) ever ran against them, so this Dockerfile is never
			// eligible for promotion: it always goes through a real `docker
			// build`, which is what actually invokes its `test` stage. See
			// erun#2090.
			out[i].GateTestStage = true
			rebuildSet[strings.TrimSpace(out[i].Image.Tag)] = struct{}{}
			continue
		}
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

func inspectFingerprintTags(build DockerBuildSpec, inspect LocalDockerImageInspector) ([]string, error) {
	if build.Fingerprint == "" {
		return nil, nil
	}
	var missing []string
	for _, platform := range build.Platforms {
		ok, err := inspect(fingerprintTag(build.Image, build.Fingerprint, platform))
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, platform)
		}
	}
	return missing, nil
}

func cascadeRebuildsThroughLocalDeps(builds []DockerBuildSpec, rebuildSet map[string]struct{}) {
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
