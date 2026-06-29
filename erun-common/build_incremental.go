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

// dockerfileCopySources returns the source paths referenced by COPY instructions
// in dockerfilePath, plus the Dockerfile path itself, all resolved relative to
// contextDir. Paths are returned in the order COPY instructions appear, with
// duplicates removed. COPY --from=<stage> instructions reference earlier build
// stages rather than the build context, so they are skipped.
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

// parseDockerfileCopyInstructions extracts the source argument list from each
// COPY instruction in a Dockerfile. The destination (last argument) is omitted.
// COPY --from=<stage> directives are skipped since they reference build stages
// rather than the host filesystem.
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
		// Drop the destination argument (last positional).
		sources := filtered[:len(filtered)-1]
		results = append(results, sources)
	}
	return results
}

// filterDockerfileCopyArgs strips COPY flags from a single instruction's
// argument list, returning the remaining positional arguments (sources plus
// destination) and whether a --from=<stage> flag was present. A --from
// instruction references a build stage rather than the host filesystem, so the
// caller skips it entirely.
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

// computeBuildFingerprint hashes the contents of the Dockerfile and every COPY
// source path resolved relative to contextDir. Files matching .dockerignore
// or .gitignore patterns are skipped so generated artifacts and untracked
// state do not perturb the result. The output is a hex-encoded SHA-256
// truncated to 16 characters — short enough to fit in a Docker tag, long
// enough to make collisions vanishingly unlikely for this use case.
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
	digest := hex.EncodeToString(hasher.Sum(nil))
	return digest[:16], nil
}

// hashFingerprintFiles folds each file's context-relative path and contents
// into the hasher, separating entries with a NUL byte so distinct file lists
// can never produce the same digest. Paths are slash-normalized so the result
// is stable across host filesystems.
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

// hashComponentChartInto folds the component's deployed Helm chart into the
// build fingerprint. The chart is a released artifact published in lockstep
// with the image at the same version (oci://<registry>/charts/<component>),
// but it lives outside the image build context — so a chart-only edit would
// otherwise leave the fingerprint unchanged, the image would promote from
// cache, and resolveDeploySkipHelm would silently drop the chart change
// ("all images cached, no rebuild"). Hashing the chart here makes a chart
// edit move the fingerprint, so the component rebuilds, republishes its chart,
// and redeploys — keeping image and chart one versioned contract. Best-effort:
// a component with no chart (erun-ubuntu, erun-dind, …) contributes nothing,
// matching the prior image-only fingerprint, and a version-pinned base image
// (its own VERSION file — erun-backend-postgres, erun-powerdns) is skipped too
// (see the guard below).
func hashComponentChartInto(w io.Writer, buildInput DockerBuildSpec) error {
	// A version-pinned base image carries its own VERSION file in its build dir
	// (erun-backend-postgres:18.3, erun-powerdns:4.9.3); its content identity is
	// that pinned upstream version, independent of any release. Its co-located
	// chart, by contrast, is version-bumped to the release version every cycle.
	// Folding the chart into the IMAGE fingerprint would churn a stable image's
	// fingerprint every release and force a needless rebuild, so skip it; the
	// base then promotes from the registry instead of rebuilding. The chart is
	// still published in lockstep — promotion re-tags and re-pushes the image at
	// the release version, and publishChartForPushedImage publishes the chart
	// alongside it; that publish does not depend on this fingerprint.
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

// componentChartDirForBuild returns the component's Helm chart directory from
// the build's known layout, without walking the repo: a charted component's
// build dir is <module>/docker/<component> and its chart is the sibling
// <module>/k8s/<component>, so the chart is derivable directly from the build's
// Dockerfile path. Identical whether computed during `erun build` or
// `erun deploy` (same DockerfilePath). Returns "" when the Dockerfile isn't in
// the conventional docker/<component> location or the component has no chart.
func componentChartDirForBuild(buildInput DockerBuildSpec) string {
	dockerfilePath := strings.TrimSpace(buildInput.DockerfilePath)
	if dockerfilePath == "" {
		return ""
	}
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(strings.TrimSpace(buildInput.ContextDir), dockerfilePath)
	}
	dockerDir := filepath.Dir(dockerfilePath) // <module>/docker/<component>
	if filepath.Base(filepath.Dir(dockerDir)) != "docker" {
		return ""
	}
	chartDir := filepath.Join(filepath.Dir(filepath.Dir(dockerDir)), "k8s", filepath.Base(dockerDir))
	if _, err := os.Stat(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		return ""
	}
	return chartDir
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

// collectFingerprintFile records a single non-directory source unless it is
// excluded by the ignore set.
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

// collectFingerprintDir walks a directory source, recording every file that
// survives the ignore set and pruning ignored subtrees so they are not visited.
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
	// base is the directory containing the .gitignore the pattern came
	// from, relative to contextDir and slash-normalized (empty for the
	// root .dockerignore / .gitignore). The matcher uses it to scope a
	// pattern to its own subtree, matching git's own behaviour where a
	// nested .gitignore only governs files beneath it.
	base string
}

type ignoreSet struct {
	patterns []ignorePattern
}

// loadContextIgnoreSet builds the ignore matcher applied during fingerprint
// computation. The root .dockerignore and .gitignore apply to every file in
// the context. .gitignore files nested under the COPY sources are also
// honoured, with each nested file's patterns scoped to its own directory
// subtree — mirroring how git itself reads .gitignore files. Missing files
// are not an error: callers receive whatever set could be parsed. Patterns
// are appended in load order so a later negation can override an earlier
// exclusion.
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

// loadNestedGitignores walks under each directory source and appends the
// patterns from every .gitignore it finds. Nested .dockerignore files are
// intentionally not honoured because Docker itself only reads the root
// one — keeping the fingerprint algorithm consistent with the actual build
// context.
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

// walkNestedGitignores walks one directory source, appending the patterns from
// every nested .gitignore it encounters to combined. visited dedupes
// directories so overlapping sources are not walked twice. Each nested file's
// patterns are scoped to its own directory subtree; the root file is skipped
// here because loadContextIgnoreSet already loaded it.
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

// parseIgnoreLine parses a single .gitignore/.dockerignore line into an
// ignorePattern scoped to base. It returns ok=false for blank lines, comments,
// and patterns that reduce to empty after stripping the negation, anchor, and
// dir-only markers.
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
	// Patterns loaded from a nested .gitignore only govern files under
	// that .gitignore's own directory. Strip the base prefix so the rest
	// of the matcher (anchoring, globbing, dir-only handling) works on a
	// path relative to where the pattern was declared.
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

// anchoredPatternMatches reports whether an anchored gitignore glob matches rel
// or any of rel's ancestor prefixes. Matching a prefix lets the walker
// short-circuit an ignored directory before visiting the directory entry
// itself.
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

// globMatch evaluates a gitignore-style glob against a single path segment or
// full path. It extends filepath.Match with `**`, which matches any sequence
// of characters including separators (e.g. `a/**/b` matches `a/b` and
// `a/x/y/b`).
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

// writeGlobToken translates the glob token starting at index i in pattern into
// its regex equivalent, writes it to b, and returns the index of the next
// unconsumed character. `**` becomes `.*` (consuming a following `/` so
// `a/**/b` matches `a/b`), `*` and `?` stay segment-local, and regex
// metacharacters are escaped.
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

// LocalDockerImageInspector reports whether an image with the given tag exists
// in the local Docker daemon. Used to detect fp-tagged images for incremental
// promotion. It is a thin wrapper over `docker image inspect` so tests can
// substitute it.
type LocalDockerImageInspector func(tag string) (bool, error)

// ApplyIncrementalToBuildExecution returns a copy of the execution with
// fingerprint-based incremental promotion applied to its docker builds. When
// noIncremental is true it returns execution unchanged. Errors during fingerprint
// computation propagate so callers can decide how to surface them.
func ApplyIncrementalToBuildExecution(ctx Context, execution BuildExecutionSpec, noIncremental bool) (BuildExecutionSpec, error) {
	updated, err := ApplyIncrementalToDockerBuilds(ctx, execution.dockerBuilds, noIncremental)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	execution.dockerBuilds = updated
	return execution, nil
}

// ApplyIncrementalToDockerBuilds applies fingerprint-based incremental
// promotion to a slice of docker builds. When noIncremental is true the slice
// is returned unchanged. This is the single entry point every command should
// use so deploy, push, and runtime deploy paths share the same skip logic as
// erun build.
//
// Builds whose image name is listed in the project's docker.fingerprints
// config get a pre-step: <image>:<VERSION> is pulled from the registry and
// tagged locally as <image>:fp-<configured>-<arch>. The downstream
// applyIncrementalPromotion then finds the tagged image and promotes the
// build. In dry-run, the pull+tag commands are traced but not executed; a
// composed inspector treats those would-be tags as present so the trace
// reflects the would-be promote path.
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

// applyIncrementalPromotion computes a fingerprint for each build and, when an
// image with the matching fingerprint tag already exists locally, marks the
// build for promotion (re-tag + push) instead of rebuild. Builds whose
// fingerprint cannot be computed, or whose fp-tagged image is missing, are left
// to rebuild as normal. A rebuilt local-base image cascades: any dependent that
// FROMs it must also rebuild, even if its own fingerprint matches.
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

// inspectFingerprintTags returns the list of platforms whose fp-tag is
// absent from the local Docker store. An empty slice means every expected
// fp-tag was found and the build is eligible for promotion. The platform
// list mirrors build.Platforms.
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
