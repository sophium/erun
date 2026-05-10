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
		fromStage := false
		filtered := make([]string, 0, len(args))
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
	ignored, err := loadContextIgnoreSet(contextDir)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	files, err := collectFingerprintFiles(contextDir, sources, ignored)
	if err != nil {
		return "", err
	}
	for _, path := range files {
		rel, err := filepath.Rel(contextDir, path)
		if err != nil {
			return "", err
		}
		rel = filepath.ToSlash(rel)
		if _, err := hasher.Write([]byte(rel)); err != nil {
			return "", err
		}
		if _, err := hasher.Write([]byte{'\n'}); err != nil {
			return "", err
		}
		if err := hashFileInto(hasher, path); err != nil {
			return "", err
		}
		if _, err := hasher.Write([]byte{0}); err != nil {
			return "", err
		}
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	return digest[:16], nil
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
			rel, err := filepath.Rel(contextDir, full)
			if err != nil {
				return nil, err
			}
			rel = filepath.ToSlash(rel)
			if !ignored.matches(rel, false) {
				add(full)
			}
			continue
		}
		walkErr := filepath.WalkDir(full, func(path string, d os.DirEntry, walkErr error) error {
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
		if walkErr != nil {
			return nil, walkErr
		}
	}
	sort.Strings(files)
	return files, nil
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
}

type ignoreSet struct {
	patterns []ignorePattern
}

// loadContextIgnoreSet reads .dockerignore and .gitignore from contextDir and
// returns a single matcher combining the two. A missing file is not an error;
// callers receive whatever set could be parsed. .dockerignore patterns are
// applied first, .gitignore second, so a later negation can override an
// earlier exclusion.
func loadContextIgnoreSet(contextDir string) (*ignoreSet, error) {
	combined := &ignoreSet{}
	for _, name := range []string{".dockerignore", ".gitignore"} {
		set, err := loadIgnoreFile(filepath.Join(contextDir, name))
		if err != nil {
			return nil, err
		}
		combined.patterns = append(combined.patterns, set.patterns...)
	}
	return combined, nil
}

func loadIgnoreFile(path string) (*ignoreSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ignoreSet{}, nil
		}
		return nil, err
	}
	return parseIgnoreData(data), nil
}

func parseIgnoreData(data []byte) *ignoreSet {
	set := &ignoreSet{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = strings.TrimSpace(line[1:])
			if line == "" {
				continue
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
			continue
		}
		set.patterns = append(set.patterns, ignorePattern{
			raw:      filepath.ToSlash(line),
			anchored: anchored,
			dirOnly:  dirOnly,
			negate:   negate,
		})
	}
	return set
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
	if p.anchored {
		if globMatch(p.raw, rel) {
			return true
		}
		// Anchored directory patterns also match descendants so the walker can
		// short-circuit before visiting the directory entry itself.
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			if globMatch(p.raw, prefix) {
				return true
			}
		}
		return false
	}
	// Non-anchored gitignore patterns match a basename anywhere in the tree.
	for _, part := range strings.Split(rel, "/") {
		if globMatch(p.raw, part) {
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
		switch {
		case i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*':
			b.WriteString(".*")
			i += 2
			// Collapse `**/` so `a/**/b` matches `a/b` as well as `a/x/b`.
			if i < len(pattern) && pattern[i] == '/' {
				i++
			}
		case pattern[i] == '*':
			b.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			b.WriteString("[^/]")
			i++
		case strings.ContainsRune(`.+()|^$\{}`, rune(pattern[i])):
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
			i++
		default:
			b.WriteByte(pattern[i])
			i++
		}
	}
	b.WriteString("$")
	return b.String()
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
