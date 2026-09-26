package eruncommon

import (
	"fmt"
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

// The erun-backend-api component's opt-in suites -- every case gated on an
// ERUN_E2E_* variable -- are inert without a venue that sets one: they report
// SKIP under a package line that still reads `ok`, which is the fail-open shape
// this whole file exists to catch, one layer up from the build fingerprints.
// The venue is that component's own `test` stage, and the two halves below are
// what make it one: the stage has to run the component's E2E gate script, and
// the builder has to depend on it through the `COPY --from=test` marker
// dockerfileHasGateTestStage reads, so no image is produced when the suite
// failed. Drop either half and every one of those cases returns to SKIP with
// `make check` still green.
//
// Naming the component rather than iterating every Dockerfile is deliberate.
// A guard phrased "any Dockerfile that ships a test stage must consume it" goes
// quiet in exactly the event it exists to catch, because deleting the stage
// leaves nothing to iterate over.
const optInE2EGateScript = "erun-devops/docker/erun-backend-api/e2e_gate_test.sh"

// testStageInstructionLines returns the instructions of the stage declared
// `FROM ... AS test`, or nothing when the file declares no such stage. The
// stage is delimited by its own FROM rather than by the next one, so a rename
// reads as "no such stage" instead of silently picking up a neighbour's body.
func testStageInstructionLines(text string) []string {
	var lines []string
	inTestStage := false
	for _, line := range strings.Split(text, "\n") {
		if dockerfileFromRefPattern.MatchString(line) {
			inTestStage = dockerfileTestStagePattern.MatchString(line)
			continue
		}
		if inTestStage {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestErunBackendAPIOptInE2ESuitesStayInAConsumedTestStage locks the venue the
// component's database-backed suites run in. The suites are opt-in on purpose
// and skipping cleanly without a venue is correct; what must not be silent is
// the venue's absence, because the loss is invisible in the only report the
// gate makes -- a package line that reads `ok` either way.
func TestErunBackendAPIOptInE2ESuitesStayInAConsumedTestStage(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	// The components are literals, not a constant run through filepath.FromSlash:
	// the repo-root read scan resolves a filepath.Join only from literal parts,
	// and reports a computed one as a read it cannot see. The name the messages
	// use is derived from this same Join so the two cannot drift.
	path := filepath.Join(root, "erun-devops", "docker", "erun-backend-api", "Dockerfile")
	venue := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !dockerfileStageRuns(dockerfileStage{lines: testStageInstructionLines(string(data))}, optInE2EGateScript) {
		t.Errorf("%s declares no `test` stage running %s — the component's ERUN_E2E_* suites are gated on a database URL, and with no venue setting one every database-backed case in them reports SKIP under a package line that still reads `ok`", venue, optInE2EGateScript)
	}
	if !dockerfileHasGateTestStage(path) {
		t.Errorf("%s no longer declares a `test` stage a later stage consumes — without the `AS test` stage and the `COPY --from=test` that depends on it the image builds and publishes with the E2E gate absent, and erun's incremental promotion is free to serve a cached fingerprint image instead of running it, so the same green build reports either way", venue)
	}
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

// devopsGateStageCommand is the command the erun-devops image runs the
// repository gate with, and the anchor the COPY model below is scoped to.
//
// The scope is the stage that runs it, not the file. A COPY in any other stage
// places nothing in the filesystem `make check` sees, so reading the whole file
// unions stages that the gate never builds: this Dockerfile's `builder` stage
// COPYs erun-devops/VERSION, the `test` stage that runs the gate does not, and a
// fatal gate test reading that path therefore passed this guard while the
// release venue — the same image, building the same stage — was red. That is one
// defect, not two: reading more of the file than the gate builds is the same
// fail-open shape as reading less of the tree than the gate reads.
const devopsGateStageCommand = "make check"

// dockerfileGateStage returns the one stage that runs the repository gate -- the
// stage whose own instructions name devopsGateStageCommand -- using the same
// FROM split the apt-package contract reads. The stage is found by what it runs
// rather than named here, so a rename keeps the model on the real stage; zero
// candidates and several are each an error rather than a silent pick, because
// either one means the model cannot tell which stage's COPYs the gate sees.
func dockerfileGateStage(data string) (dockerfileStage, error) {
	var found []dockerfileStage
	for _, stage := range dockerfileStages(data) {
		if dockerfileStageRuns(stage, devopsGateStageCommand) {
			found = append(found, stage)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return dockerfileStage{}, fmt.Errorf("declares no stage running %q: this guard models the COPYs of the stage "+
			"that runs the repository gate, and with no such stage it cannot tell which stage's COPYs the gate sees "+
			"— restore the stage, or update devopsGateStageCommand to whatever now runs it", devopsGateStageCommand)
	default:
		return dockerfileStage{}, fmt.Errorf("declares %d stages running %q: this guard models the COPYs of the one "+
			"stage the gate runs in, and with several candidates it cannot tell which stage's COPYs the gate sees "+
			"— name the gate command in exactly one stage", len(found), devopsGateStageCommand)
	}
}

// dockerfileStageRuns reports whether a stage runs a command, reading only its
// instructions. Comment lines are skipped: this Dockerfile's builder stage has a
// comment explaining that a `make check` elsewhere runs golangci-lint for it,
// and a prose mention is not a stage running the gate — matching raw text would
// make that stage a second candidate and leave the model unable to pick one.
func dockerfileStageRuns(stage dockerfileStage, command string) bool {
	for _, line := range stage.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, command) {
			return true
		}
	}
	return false
}

// dockerfileGateStageLines returns the lines of that stage, failing the test
// when the Dockerfile does not declare exactly one.
func dockerfileGateStageLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	stage, err := dockerfileGateStage(string(data))
	if err != nil {
		t.Fatalf("%s %v", path, err)
	}
	return stage.lines
}

// erunDevopsProvidedSrcPaths returns every container path under /src that the
// gate stage's COPY instructions provide. A destination that is a directory
// lands each source under it by name, so `COPY package.json yarn.lock /src/`
// provides /src/package.json and /src/yarn.lock and not the bare workdir;
// /src itself is never returned, because "the image provides /src" would
// satisfy every path either half of this guard asks about and make both
// vacuous.
//
// contextRoot is the build context the Dockerfile's relative sources resolve
// against — the repository root for the real image, the fixture root for a
// fixture tree — and is what lets a directory source be told from a file one.
//
// COPY --from=<stage> lines are skipped: their sources are another stage's
// filesystem rather than this build context, so they place nothing the
// checkout supplied. computeBuildFingerprint drops them for the same reason.
func erunDevopsProvidedSrcPaths(t *testing.T, contextRoot, path string) []string {
	t.Helper()
	var provided []string
	for _, line := range dockerfileGateStageLines(t, path) {
		provided = append(provided, dockerfileCopyProvidedSrcPaths(contextRoot, line)...)
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
// instruction provides, following what a real docker build does with each
// source rather than a single rule for both kinds.
//
// The basename level is a *file* source's rule: `COPY package.json yarn.lock
// /src/` provides /src/package.json and /src/yarn.lock, and `COPY a.txt
// /src/one/` provides /src/one/a.txt. A *directory* source contributes its
// contents at the destination instead, with no extra level: `COPY some-dir
// /src/some-dir/` lands /src/some-dir/<contents>, not /src/some-dir/some-dir,
// and `COPY a.txt dir /src/three/` puts a.txt at /src/three/a.txt and dir's
// contents directly under /src/three. Verified against a real docker build.
//
// Treating a directory source as a file is not a conservative mistake: it
// reports a path the image really does provide as absent, so the guard reds a
// change that is correct. Both directions are defects, and this one is cheaper
// to hit -- the destination form `COPY <dir> /src/<dir>/` is ordinary.
func dockerfileCopyProvidedSrcPaths(contextRoot, line string) []string {
	sources, dest, ok := dockerfileCopyArgs(line)
	if !ok {
		return nil
	}
	if !strings.HasSuffix(dest, "/") && len(sources) == 1 && !dockerfileSourceIsDirectory(contextRoot, sources[0]) {
		return []string{dest}
	}
	dir := strings.TrimSuffix(dest, "/")
	var provided []string
	for _, source := range sources {
		if dockerfileSourceIsDirectory(contextRoot, source) {
			provided = append(provided, dir)
			continue
		}
		provided = append(provided, dir+"/"+filepath.Base(strings.TrimSuffix(source, "/")))
	}
	return provided
}

// dockerfileSourceIsDirectory reports whether a COPY source names a directory in
// the build context. A source this checkout does not contain is treated as a
// file, which is the destination rule that places it under the destination by
// name: every source of the real Dockerfile exists here, and a fixture tree
// names sources it deliberately never writes.
func dockerfileSourceIsDirectory(contextRoot, source string) bool {
	if contextRoot == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(contextRoot, filepath.FromSlash(source)))
	return err == nil && info.IsDir()
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
// COPY-or-`cd`-fails rule the Makefile states for LINT_MODULES to the shell
// scripts check-gate runs, in both directions: the script file the recipe
// invokes by path, and the repository-root directory each script resolves from
// its own script_dir (`cd "${script_dir}/../../../<path>"`).
//
// Both are directories this stage has to provide for the same reason a linted
// module does -- and unlike a missing module, which fails lint loudly, a missing
// script or directory fails only that one target, inside the image, while the
// identical command passes in a full checkout.
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
// Three limits are worth stating rather than leaving to be discovered, and all
// three are the same class this test closes for the ordinary case.
//
// The widest is a path that arrives as a function *parameter*. Nothing here
// traces a helper back to its callers, so a read performed inside a helper stays
// outside this check however the read is written -- and that is not a corner of
// the codebase: `func readX(t testing.TB, path string) string` is one of the
// commonest helper shapes in these modules, and the very read this scan was
// extended for (erun-ui/buildstamp_test.go's Formula/bucket pair) is passed
// through exactly such a helper and opened inside it. What the CWD-relative
// resolution makes visible is the path *expression* at the site that names it;
// the read itself is still inside a function this scan never binds an argument
// into. The same applies to a helper taking the repository root and building
// paths under it, which is the shape this file's own fixture pins as uncovered.
//
// The second is path construction that never goes through filepath.Join: a
// fmt.Sprintf, a string concatenation, a struct field or slice element holding a
// path, an os.ReadFile of something assembled elsewhere. The scan keys on
// filepath.Join, so none of those is even a candidate.
//
// The third is a Join the scan declines to resolve: a non-".."-leading literal
// first argument, an absolute one, or one that climbs past the checkout. Those
// are excluded because they cannot reach repository-root state, not because they
// were examined and found safe.
//
// None of the three is silent. The first two are named here and pinned by
// TestScanLeavesParameterRootedAndNonJoinReadsUnseen, so a change that makes
// them visible reconciles this text instead of contradicting it; the third is
// stated at gateGoTestCwdRelativeRead. What the test does not do is fail on any
// of them.
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
	provided := erunDevopsProvidedSrcPaths(t, root, filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	for _, script := range checkGateShellScripts(t, root) {
		// The script file itself first: the Makefile invokes it by path, so a
		// stage that copies the directory a script cd's into but not the script
		// is a gate that cannot start. Only the cd targets were checked before,
		// which left a script under a path this stage does not COPY passing a
		// guard whose stated job was to check the image provides it.
		if !providedSrcPathCovers(provided, "/src/"+script) {
			t.Errorf("%s is run by the Makefile's check-gate targets, but the erun-devops image test stage COPYs nothing that provides /src/%s — `make check` passes in a full checkout and fails inside every image build, so no release can be produced", script, script)
		}
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
