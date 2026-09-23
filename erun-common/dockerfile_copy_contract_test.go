package eruncommon

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// realDockerfilePaths returns every Dockerfile erun actually builds from —
// erun-devops/docker/<component>/Dockerfile — excluding the erun-skills
// scaffolding templates, which use __PLACEHOLDER__ tokens instead of real
// paths and are never fed to computeBuildFingerprint.
func realDockerfilePaths(t *testing.T) []string {
	t.Helper()
	root := repoRootForDockerignoreTest(t)
	matches, err := filepath.Glob(filepath.Join(root, "erun-devops", "docker", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("glob Dockerfiles: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Dockerfiles found under erun-devops/docker")
	}
	sort.Strings(matches)
	return matches
}

var dockerfileAddPattern = regexp.MustCompile(`(?im)^\s*ADD\s+`)

// TestDockerfilesNeverUseAddForLocalContent locks the fact that no Dockerfile
// uses ADD today. dockerfileCopySources (build_incremental.go) parses only
// COPY instructions (parseDockerfileCopyInstructions matches literal "COPY"),
// so an ADD source's local file/dir would never become a fingerprint input —
// unlike the mirrored-.gitignore bug, this would not even self-correct once:
// the Dockerfile text change that introduces the ADD line moves the
// fingerprint once, but every later edit to the ADDed content afterward would
// not, since that content was never added to computeBuildFingerprint's source
// list. Introducing ADD is a deliberate decision that must first teach
// dockerfileCopySources to parse it, not a silent gap this test lets slide.
func TestDockerfilesNeverUseAddForLocalContent(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if dockerfileAddPattern.MatchString(string(data)) {
			t.Errorf("%s uses ADD: computeBuildFingerprint only parses COPY instructions, so an ADD source's content would never enter the build fingerprint even though the real docker build copies it — teach dockerfileCopySources to parse ADD before using it here", path)
		}
	}
}

// TestDockerfileCopySourcesContainNoGlobs locks the fact that no COPY source
// uses a shell-style glob today. collectFingerprintFiles (build_incremental.go)
// resolves each source with os.Lstat on the literal string; a glob like
// "*.json" does not exist as a literal path, so os.ErrNotExist is treated as
// "nothing to hash" and the source is silently dropped — while the real
// docker build expands the glob and copies every match. That is the fail-open
// shape this whole audit is about: files enter the image without ever moving
// the fingerprint that decides whether it rebuilds.
func TestDockerfileCopySourcesContainNoGlobs(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, path := range realDockerfilePaths(t) {
		sources, err := dockerfileCopySources(path, root)
		if err != nil {
			t.Fatalf("parse COPY sources for %s: %v", path, err)
		}
		for _, src := range sources {
			if strings.ContainsAny(src, "*?[") {
				t.Errorf("%s: COPY source %q contains a glob character — computeBuildFingerprint would silently skip it as a nonexistent literal path while the real docker build expands and copies the matches", path, src)
			}
		}
	}
}

var (
	dockerfileStageNamePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+(\S+)`)
	dockerfileCopyFromPattern  = regexp.MustCompile(`(?im)^\s*COPY\s+.*?--from=(\S+)`)
)

// TestDockerfileCopyFromReferencesOnlyLocalStages locks the assumption behind
// filterDockerfileCopyArgs (build_incremental.go): every "COPY --from=X" is
// treated unconditionally as a reference to a build stage in the same file
// and dropped from the fingerprint entirely, since a stage's content is
// already governed by that stage's own FROM/COPY chain. That assumption holds
// only while X is genuinely a declared stage (or a numeric stage index) — if
// X ever named an external image instead, that image's content would become
// an unfingerprinted build input with no cascade to catch it (dockerfileLocalBaseImageDeps
// only walks FROM lines, never COPY --from).
func TestDockerfileCopyFromReferencesOnlyLocalStages(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		stages := make(map[string]struct{})
		for _, m := range dockerfileStageNamePattern.FindAllStringSubmatch(text, -1) {
			stages[strings.ToLower(m[1])] = struct{}{}
		}
		for _, m := range dockerfileCopyFromPattern.FindAllStringSubmatch(text, -1) {
			ref := strings.ToLower(m[1])
			if _, ok := stages[ref]; ok {
				continue
			}
			if isDigits(ref) {
				continue // numeric stage index (COPY --from=0 ...)
			}
			t.Errorf("%s: COPY --from=%s does not name a stage declared in this file (FROM ... AS %s) — filterDockerfileCopyArgs drops every --from as a stage reference unconditionally, so if this is really an external image its content is invisible to computeBuildFingerprint even though the real build copies from it", path, m[1], m[1])
		}
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var dockerfileFromRefPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// TestDockerfileBaseImagesUseExplicitPinnedTags enforces the one part of the
// base-image-drift risk a fast, static test actually can. computeBuildFingerprint
// hashes the Dockerfile's own bytes, so it catches a base image reference
// changing in the Dockerfile text — but it hashes the tag string, never the
// digest that tag resolves to, so a mutable upstream tag (a registry
// republishing e.g. alpine:3.20 under the same tag) moves underneath an
// already-cached fingerprint with nothing to catch it. Because a
// fingerprint-matched build promotes a previously-built image instead of ever
// invoking docker build again, that drift ships with no rebuild at all.
// Resolving every base image's live digest on every build would close it
// fully, but costs a network round-trip this repo's caching model exists to
// avoid paying (see root AGENTS.md's ~9-minute figure) — so the accepted
// mitigation is pinned-tag discipline (root AGENTS.md "Release Rules"), and
// this test is what keeps that discipline from regressing silently.
func TestDockerfileBaseImagesUseExplicitPinnedTags(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		stages := make(map[string]struct{})
		for _, m := range dockerfileStageNamePattern.FindAllStringSubmatch(text, -1) {
			stages[strings.ToLower(m[1])] = struct{}{}
		}
		for _, m := range dockerfileFromRefPattern.FindAllStringSubmatch(text, -1) {
			ref := m[1]
			if dockerfileFromRefIsPinned(ref, stages) {
				continue
			}
			t.Errorf("%s: FROM %s has no explicit pinned tag — an untagged or \"latest\" base image can mutate upstream with nothing in the repo moving the fingerprint that gates rebuilds", path, ref)
		}
	}
}

// dockerfileFromRefIsPinned reports whether a FROM reference is either not a
// real external image (scratch, or a previous stage in the same file) or
// names an explicit tag/digest rather than a floating "latest".
func dockerfileFromRefIsPinned(ref string, stages map[string]struct{}) bool {
	lower := strings.ToLower(ref)
	if lower == "scratch" {
		return true
	}
	if _, ok := stages[lower]; ok {
		return true // FROM of a previous stage in this file, not an image
	}
	if strings.Contains(ref, "@sha256:") {
		return true // digest-pinned, strictly stronger than a tag
	}
	nameAndTag := ref
	if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
		nameAndTag = ref[lastSlash+1:]
	}
	tag := ""
	if colon := strings.LastIndex(nameAndTag, ":"); colon >= 0 {
		tag = nameAndTag[colon+1:]
	}
	return tag != "" && tag != "latest"
}

var (
	makefileRulePattern       = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*::?\s*(.*)$`)
	makefileShellScriptToken  = regexp.MustCompile(`(?:^|\s)(\S+\.sh)(?:\s|$)`)
	scriptRelativeRootPattern = regexp.MustCompile(`\$\{?script_dir\}?/(?:\.\./)+([A-Za-z0-9_][A-Za-z0-9_./-]*)`)
)

type makefileRule struct {
	prereqs []string
	recipe  []string
}

// makefileRules reads the Makefile into one entry per target, keeping its
// prerequisites and its tab-indented recipe lines apart -- the recipe is what
// names the shell scripts a target actually runs.
func makefileRules(t *testing.T, root string) map[string]makefileRule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	rules := make(map[string]makefileRule)
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "\t") {
			if current != "" {
				rule := rules[current]
				rule.recipe = append(rule.recipe, line)
				rules[current] = rule
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		match := makefileRulePattern.FindStringSubmatch(line)
		if match == nil {
			current = ""
			continue
		}
		current = match[1]
		rule := rules[current]
		rule.prereqs = append(rule.prereqs, strings.Fields(match[2])...)
		rules[current] = rule
	}
	return rules
}

// checkGateShellScripts returns every shell script the Makefile's check-gate
// prerequisite targets name and that really exists in the repository, in a
// stable order. A token that resolves to no file is skipped rather than
// reported: a recipe also writes `./run.sh` after a `cd` into another
// directory, and `erun-devops/k8s/*_test.sh` as a shell glob, neither of which
// is a repository-root path — and a script that is genuinely missing fails the
// gate loudly on its own, without this test's help.
func checkGateShellScripts(t *testing.T, root string) []string {
	t.Helper()
	rules := makefileRules(t, root)
	gate, ok := rules["check-gate"]
	if !ok {
		t.Fatal("Makefile declares no check-gate target")
	}
	seen := make(map[string]struct{})
	var scripts []string
	for _, prereq := range gate.prereqs {
		for _, line := range rules[prereq].recipe {
			for _, match := range makefileShellScriptToken.FindAllStringSubmatch(line, -1) {
				script := strings.TrimPrefix(strings.TrimSpace(match[1]), "./")
				if _, dup := seen[script]; dup {
					continue
				}
				if info, err := os.Stat(filepath.Join(root, script)); err != nil || info.IsDir() {
					continue
				}
				seen[script] = struct{}{}
				scripts = append(scripts, script)
			}
		}
	}
	sort.Strings(scripts)
	return scripts
}

// erunDevopsProvidedSrcPaths returns every container path under /src that the
// Dockerfile's COPY instructions provide. A destination that is a directory
// lands each source under it by name, so `COPY package.json yarn.lock /src/`
// provides /src/package.json and /src/yarn.lock and not the bare workdir;
// /src itself is never returned, because "the image provides /src" would
// satisfy every path either half of this guard asks about and make both
// vacuous.
//
// COPY --from=<stage> lines are skipped: their sources are another stage's
// filesystem rather than this build context, so they place nothing the
// checkout supplied. computeBuildFingerprint drops them for the same reason.
func erunDevopsProvidedSrcPaths(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var provided []string
	for _, line := range strings.Split(string(data), "\n") {
		provided = append(provided, dockerfileCopyProvidedSrcPaths(line)...)
	}
	var underSrc []string
	for _, candidate := range provided {
		if strings.HasPrefix(candidate, "/src/") && candidate != "/src" {
			underSrc = append(underSrc, candidate)
		}
	}
	return underSrc
}

// dockerfileCopyArgs splits one Dockerfile line into a COPY instruction's
// sources and its destination. It reports ok=false for anything else --
// another instruction, a copy from another stage (--from=), or a line with no
// destination to speak of.
func dockerfileCopyArgs(line string) (sources []string, dest string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < len("COPY ") || !strings.EqualFold(trimmed[:len("COPY ")], "COPY ") {
		return nil, "", false
	}
	var tokens []string
	for _, field := range strings.Fields(trimmed)[1:] {
		if strings.HasPrefix(field, "--from=") {
			return nil, "", false
		}
		if strings.HasPrefix(field, "--") {
			continue // --chmod= and friends are not paths
		}
		tokens = append(tokens, field)
	}
	if len(tokens) < 2 {
		return nil, "", false
	}
	return tokens[:len(tokens)-1], tokens[len(tokens)-1], true
}

// dockerfileCopyProvidedSrcPaths returns the container paths one COPY
// instruction provides. A destination that is a directory lands each source
// under it by name, so `COPY package.json yarn.lock /src/` provides
// /src/package.json and /src/yarn.lock; a single source with any other
// destination lands at that destination, whether it is a file or a directory.
func dockerfileCopyProvidedSrcPaths(line string) []string {
	sources, dest, ok := dockerfileCopyArgs(line)
	if !ok {
		return nil
	}
	if !strings.HasSuffix(dest, "/") && len(sources) == 1 {
		return []string{dest}
	}
	dir := strings.TrimSuffix(dest, "/")
	var provided []string
	for _, source := range sources {
		provided = append(provided, dir+"/"+filepath.Base(strings.TrimSuffix(source, "/")))
	}
	return provided
}

// providedSrcPathExists reports whether the image provides wanted itself or
// anything beneath it. A script only has to `cd` into the directory, and
// copying a deeper path creates every parent on the way down.
func providedSrcPathExists(provided []string, wanted string) bool {
	for _, path := range provided {
		if path == wanted || strings.HasPrefix(path, wanted+"/") {
			return true
		}
	}
	return false
}

// providedSrcPathCovers reports whether the image ends up holding path -- the
// file-read direction, the mirror of providedSrcPathExists above. A read needs
// path itself or any directory above it: copying a directory puts everything
// under it in the image.
func providedSrcPathCovers(provided []string, path string) bool {
	for _, candidate := range provided {
		if candidate == path || strings.HasPrefix(path, candidate+"/") {
			return true
		}
	}
	return false
}

// providedSrcPathCoversRead answers the same question for a read whose path may
// run through a glob (`erun-devops/docker/*/Dockerfile`). What such a read
// needs is the directory the glob expands inside -- the part of the path before
// its first glob-bearing component -- and the guard reports a path that globs
// from its very first component rather than passing it.
func providedSrcPathCoversRead(provided []string, path string) bool {
	components := strings.Split(path, "/")
	for i, component := range components {
		if !strings.ContainsAny(component, "*?[") {
			continue
		}
		if i == 0 {
			return false
		}
		return providedSrcPathCovers(provided, strings.Join(components[:i], "/"))
	}
	return providedSrcPathCovers(provided, path)
}

// TestCheckGateScriptsResolveOnlyDirectoriesTheDevopsImageProvides extends the
// COPY-or-`cd`-fails rule the Makefile states for LINT_MODULES beyond the Go
// modules it lints, to the shell scripts check-gate runs. Those scripts
// resolve a repository root from their own script_dir
// (`cd "${script_dir}/../../../<path>"`), which is a directory this stage has
// to provide for the same reason a linted module does -- and unlike a missing
// module, which fails lint loudly, a missing directory fails only the one
// script, inside the image, while the identical command passes in a full
// checkout.
//
// That is exactly how test-atlas-validate shipped: the target, its script and
// the module's own Dockerfile all landed together, but the erun-devops image --
// whose test stage is what runs `make check` -- was never taught to COPY the
// module the new script cd's into. The result was a gate that passed for every
// contributor and on every PR, and a `make check` that could not succeed inside
// any erun-devops image build, which is every release build. Nothing here is
// release-specific: this test reads the Makefile and the Dockerfile, so it
// fails in the same run that introduces the next one.
// TestCheckGateGoTestsReadOnlyRepoRootPathsTheDevopsImageProvides is the
// Go-test half of the same contract. A gate test reads repo-root state through
// a runtime.Caller-derived helper rather than through a shell script's
// script_dir, so the shell-script half above cannot see it: that guard's scan
// set is Makefile-named `.sh` files, and the path a Go test resolves never
// appears in one.
//
// That gap is exactly how a release came to be blocked by an absent COPY:
// erun-integration/gitignore_clean_checkout_test.go read the checkout's
// .gitignore through repoRoot, which resolves to this stage's /src, and the
// read is fatal rather than a skip. `make check` aborted at
// integration-test-gate inside the Docker venue while the identical tree passed
// in a pod checkout where the file is simply present -- and the shell-script
// half of this guard passed on that same tree, because it never looked at a Go
// test.
//
// The failure direction is closed, deliberately: a read this scan can resolve
// must be something the test stage COPYs, and a read it cannot resolve is
// reported rather than skipped, so a site that escapes the scan can never be
// counted as satisfied. The cost is registration -- every unresolvable read,
// and every resolved one the image genuinely does not provide, needs an entry
// in goTestRepoRootReadExemptions with the reason for it, and an entry that
// matches no site fails this test.
//
// Two limits are worth stating rather than leaving to be discovered. A read
// whose root arrives as a function parameter (`filepath.Join(root, ...)` inside
// a helper that takes `root string`) is not traced back to its callers, so a
// new read added inside one of those helpers is outside this check. So is path
// construction that never goes through filepath.Join -- a fmt.Sprintf, an
// os.ReadFile of a path assembled elsewhere. Both are the same class this test
// closes for the ordinary case; neither is covered.
func TestCheckGateGoTestsReadOnlyRepoRootPathsTheDevopsImageProvides(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	findings, scan := gateGoTestCopyContractFindings(t, root, filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"), gateGoTestModuleDirs, goTestRepoRootReadExemptions)
	for _, finding := range findings {
		t.Error(finding)
	}
	for _, helper := range gateGoTestRepoRootHelperNames {
		if !scan.repoRootHelpers[helper] {
			t.Errorf("%s is not recognized as resolving the repository root -- either it was renamed or its return "+
				"shape changed, and the scan that finds reads through it is now looking for something that is no "+
				"longer there; update this list and the detection together", helper)
		}
	}
}

func TestCheckGateScriptsResolveOnlyDirectoriesTheDevopsImageProvides(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	provided := erunDevopsProvidedSrcPaths(t, filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	for _, script := range checkGateShellScripts(t, root) {
		data, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatalf("read %s: %v", script, err)
		}
		for _, match := range scriptRelativeRootPattern.FindAllStringSubmatch(string(data), -1) {
			wanted := "/src/" + filepath.ToSlash(filepath.Clean(match[1]))
			if providedSrcPathExists(provided, wanted) {
				continue
			}
			t.Errorf("%s resolves %q relative to its own location, but the erun-devops image test stage COPYs nothing under %s — `make check` passes in a full checkout and fails inside every image build, so no release can be produced", script, match[1], wanted)
		}
	}
}
