package build

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// realDockerfilePaths returns every image Dockerfile erun actually builds
// from — erun-devops/docker/<component>/Dockerfile — excluding the erun-skills
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

// linuxPackageDockerfilePaths returns every package component's Dockerfile,
// erun-devops/linux/<package>/Dockerfile, which builds a .deb, never an image.
func linuxPackageDockerfilePaths(t *testing.T) []string {
	t.Helper()
	root := repoRootForDockerignoreTest(t)
	matches, err := filepath.Glob(filepath.Join(root, "erun-devops", "linux", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("glob package Dockerfiles: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Dockerfiles found under erun-devops/linux")
	}
	sort.Strings(matches)
	return matches
}

// allDockerfilePaths is every Dockerfile of this repository, images and
// packages alike, for the rules that hold for any Dockerfile.
func allDockerfilePaths(t *testing.T) []string {
	t.Helper()
	return append(realDockerfilePaths(t), linuxPackageDockerfilePaths(t)...)
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
	for _, path := range allDockerfilePaths(t) {
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
	for _, path := range allDockerfilePaths(t) {
		sources, err := DockerfileCopySources(path, root)
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
	for _, path := range allDockerfilePaths(t) {
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
	for _, path := range allDockerfilePaths(t) {
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

// The erun-backend-api component's tests -- its database suites included, which
// start PostgreSQL themselves -- run as part of building that image, and the two
// halves below are what make them so: the `test` stage has to run the module's
// own `go test`, and a later stage has to consume it (`FROM test` or
// `COPY --from=test`, which dockerfileHasConsumedTestStage reads), so no image
// is produced when the suite failed. Drop either half and the image builds and
// publishes with its tests never run.
//
// Naming the component rather than iterating every Dockerfile is deliberate.
// A guard phrased "any Dockerfile that ships a test stage must consume it" goes
// quiet in exactly the event it exists to catch, because deleting the stage
// leaves nothing to iterate over.
const componentTestCommand = "go test -count=1 ./..."

// testStageInstructionLines returns the instructions of the stage declared
// `FROM ... AS test`, or nothing when the file declares no such stage. The
// stage is delimited by its own FROM rather than by the next one, so a rename
// reads as "no such stage" instead of silently picking up a neighbour's body.
func testStageInstructionLines(text string) []string {
	var lines []string
	inTestStage := false
	for _, line := range strings.Split(text, "\n") {
		if dockerfileFromRefPattern.MatchString(line) {
			inTestStage = shared.DockerfileTestStagePattern.MatchString(line)
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
	if !dockerfileStageRuns(dockerfileStage{lines: testStageInstructionLines(string(data))}, componentTestCommand) {
		t.Errorf("%s declares no `test` stage running %s — the component's tests, its database suites included, would never run before its image is built", venue, componentTestCommand)
	}
	if !shared.DockerfileHasConsumedTestStage(path) {
		t.Errorf("%s no longer declares a `test` stage a later stage consumes — without the `AS test` stage and a later stage built or copying from it the image builds and publishes with its tests never run, and a fingerprint written after that build would let later builds reuse an untested result", venue)
	}
}

// dockerfileStageRuns reports whether a stage runs a command, reading only its
// instructions. Comment lines are skipped: a prose mention of a command is not
// a stage running it.
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

// componentDockerfiles are the Dockerfiles under erun-devops/docker that build
// and test their own content, as opposed to the single-stage wrappers that pass
// an upstream image through. Named rather than discovered for the reason given
// above: a rule phrased over "every multi-stage Dockerfile" goes quiet when a
// component collapses to one stage.
var componentDockerfiles = []string{
	"erun-backend-api",
	"erun-backend-db",
	"erun-console",
	"erun-devops",
	"erun-dns01-webhook",
	"erun-docs",
}

// linuxPackageDockerfiles are the package components under erun-devops/linux,
// each mapped to whether it tests itself. erun-cli does not: the CLI and the
// MCP server are tested in the erun-devops image's cli-test, so its package
// only compiles and packs them.
var linuxPackageDockerfiles = map[string]bool{
	"erun-cli": false,
	"erun-ui":  true,
}

var (
	dockerfileStageHeaderPattern  = regexp.MustCompile(`(?i)^\s*FROM\s+(--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?\s*$`)
	dockerfileStageReferenceArg   = regexp.MustCompile(`(?i)(?:^|[\s,])(?:--)?from=([^\s,]+)`)
	dockerfileStampOrTargetArgDef = regexp.MustCompile(`(?i)^\s*ARG\s+(TARGET\w*|ERUN_VERSION)\b`)
)

type componentStage struct {
	name     string
	base     string
	platform string
	lines    []string
}

func componentStagesOf(text string) []componentStage {
	var stages []componentStage
	for _, raw := range strings.Split(text, "\n") {
		if m := dockerfileStageHeaderPattern.FindStringSubmatch(raw); m != nil {
			stages = append(stages, componentStage{name: strings.ToLower(m[3]), base: strings.ToLower(m[2]), platform: strings.TrimSpace(m[1])})
			continue
		}
		if len(stages) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		stages[len(stages)-1].lines = append(stages[len(stages)-1].lines, raw)
	}
	return stages
}

// stageReferences returns the stages a stage's instructions read from
// (`COPY --from=<stage>`, `RUN --mount=...,from=<stage>`).
func (s componentStage) stageReferences() []string {
	var refs []string
	for _, line := range s.lines {
		for _, m := range dockerfileStageReferenceArg.FindAllStringSubmatch(line, -1) {
			refs = append(refs, strings.ToLower(m[1]))
		}
	}
	return refs
}

// componentStageConventionProblems checks a component Dockerfile against the
// repository's stage convention, build -> test -> helpers -> runtime, and
// returns one line per violation:
//
//   - a build stage (`build`, or `<part>-build` in a Dockerfile with several
//     parts) holds the toolchain, dependencies, source and a native compile,
//     pinned to BUILDPLATFORM, with no version stamping and no TARGET* args,
//     so the tests built on it run once per source change rather than once
//     per version or architecture;
//   - its test stage (`test`, or `<part>-test`) is FROM it, stamps no version
//     either, and is the only stage that reads it, so nothing can take an
//     artifact around the tests;
//   - the stage named `test` is the one erun detects: every `<part>-test`
//     feeds it, and every helper built from a stage of this file descends
//     from it;
//   - the last stage is `runtime` and reaches `test`, which keeps the tests in
//     the graph BuildKit walks for the image.
//
// A stage FROM an external image that reads no other stage is a toolchain
// source (a target-arch toolchain, a pinned node) and is allowed as is.
func componentStageConventionProblems(text string) []string {
	return stageConventionProblems(text, "runtime", true)
}

// packageStageConventionProblems checks a package component's Dockerfile: the
// same chain, ending in `dist` instead of `runtime`, FROM scratch so a local
// output export writes the package files and nothing else. A package tested
// elsewhere declares no test stage; its build stages still stamp nothing.
func packageStageConventionProblems(text string, tested bool) []string {
	problems := stageConventionProblems(text, "dist", tested)
	stages := componentStagesOf(text)
	if len(stages) > 0 && stages[len(stages)-1].base != "scratch" {
		problems = append(problems, "the last stage is FROM "+stages[len(stages)-1].base+", want FROM scratch so the exported output holds only the package")
	}
	return problems
}

func stageConventionProblems(text, lastStage string, requireTest bool) []string {
	stages := componentStagesOf(text)
	if len(stages) == 0 {
		return []string{"no FROM instruction"}
	}
	graph := newComponentStageGraph(stages)
	if _, hasTest := graph.byName["test"]; !hasTest {
		if requireTest {
			return []string{"no stage named `test`"}
		}
		return graph.untestedProblems(lastStage)
	}
	var problems []string
	for _, stage := range stages {
		problems = append(problems, graph.stageProblems(stage)...)
	}
	return append(problems, graph.lastStageProblems(lastStage)...)
}

// untestedProblems checks a Dockerfile with no test stage: its build stages
// stay free of version stamps and target-arch args, and its last stage is
// named for what it ships.
func (g componentStageGraph) untestedProblems(lastStage string) []string {
	var problems []string
	for _, stage := range g.stages {
		if part, role := stageRole(stage.name); role == "build" {
			problems = append(problems, g.buildStageProblems(stage, part)...)
		}
	}
	return append(problems, g.lastStageNameProblems(lastStage)...)
}

type componentStageGraph struct {
	stages []componentStage
	byName map[string]componentStage
}

func newComponentStageGraph(stages []componentStage) componentStageGraph {
	byName := make(map[string]componentStage)
	for _, stage := range stages {
		if stage.name != "" {
			byName[stage.name] = stage
		}
	}
	return componentStageGraph{stages: stages, byName: byName}
}

// stageRole splits a build or test stage name into its part and role:
// `build` is ("", "build"), `cli-test` is ("cli", "test"). Any other stage
// has no role.
func stageRole(name string) (part, role string) {
	for _, r := range []string{"build", "test"} {
		if name == r {
			return "", r
		}
		if prefix, ok := strings.CutSuffix(name, "-"+r); ok && prefix != "" {
			return prefix, r
		}
	}
	return "", ""
}

func partStageName(part, role string) string {
	if part == "" {
		return role
	}
	return part + "-" + role
}

// reads returns the stages of this file a stage builds on or reads from.
func (g componentStageGraph) reads(stage componentStage) []string {
	var local []string
	for _, name := range append([]string{stage.base}, stage.stageReferences()...) {
		if _, ok := g.byName[name]; ok {
			local = append(local, name)
		}
	}
	return local
}

// reaches reports whether a stage depends, directly or through other stages of
// this file, on the named one.
func (g componentStageGraph) reaches(from componentStage, target string) bool {
	seen := map[string]bool{}
	pending := g.reads(from)
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if name == target {
			return true
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		pending = append(pending, g.reads(g.byName[name])...)
	}
	return false
}

// descendsFromTest reports whether the named stage is `test` or built, through
// stages of this file, FROM it.
func (g componentStageGraph) descendsFromTest(name string) bool {
	for range g.stages {
		if name == "test" {
			return true
		}
		stage, ok := g.byName[name]
		if !ok {
			return false
		}
		name = stage.base
	}
	return name == "test"
}

func stampOrTargetArgProblems(stage componentStage, consequence string) []string {
	var problems []string
	for _, line := range stage.lines {
		if dockerfileStampOrTargetArgDef.MatchString(line) {
			problems = append(problems, "stage `"+stage.name+"` declares "+strings.TrimSpace(line)+consequence)
		}
	}
	return problems
}

func (g componentStageGraph) stageProblems(stage componentStage) []string {
	problems := g.buildReadProblems(stage)
	part, role := stageRole(stage.name)
	switch role {
	case "build":
		return append(problems, g.buildStageProblems(stage, part)...)
	case "test":
		return append(problems, g.testStageProblems(stage, part)...)
	}
	if _, local := g.byName[stage.base]; local && !g.descendsFromTest(stage.base) {
		problems = append(problems, "stage "+strconv.Quote(stage.name)+" is FROM "+stage.base+", which does not descend from `test`; helpers are FROM test")
	}
	return problems
}

// buildReadProblems reports a stage that reads a build stage it is not the
// test stage of, which would let an artifact reach the image around the tests.
func (g componentStageGraph) buildReadProblems(stage componentStage) []string {
	var problems []string
	for _, name := range g.reads(stage) {
		part, role := stageRole(name)
		if role != "build" || stage.name == partStageName(part, "test") {
			continue
		}
		problems = append(problems, "stage "+strconv.Quote(stage.name)+" reads from `"+name+"`; only `"+partStageName(part, "test")+"` builds on it, so take the artifact from `test` or a helper FROM test, or BuildKit can skip the tests")
	}
	return problems
}

func (g componentStageGraph) buildStageProblems(stage componentStage, part string) []string {
	var problems []string
	if stage.platform != "--platform=$BUILDPLATFORM" {
		problems = append(problems, "stage `"+stage.name+"` is not pinned with --platform=$BUILDPLATFORM")
	}
	_, tested := g.byName["test"]
	if _, ok := g.byName[partStageName(part, "test")]; tested && !ok {
		problems = append(problems, "stage `"+stage.name+"` has no `"+partStageName(part, "test")+"` built on it")
	}
	return append(problems, stampOrTargetArgProblems(stage, ": version stamping and target-arch compiles belong in a helper stage FROM test, or the tests rerun per version and per architecture")...)
}

func (g componentStageGraph) testStageProblems(stage componentStage, part string) []string {
	var problems []string
	build := partStageName(part, "build")
	if _, ok := g.byName[build]; ok && stage.base != build {
		problems = append(problems, "stage `"+stage.name+"` is FROM "+stage.base+", want FROM "+build)
	}
	if part != "" && !g.reaches(g.byName["test"], stage.name) {
		problems = append(problems, "stage `"+stage.name+"` does not feed `test`, so BuildKit prunes it from the image's graph")
	}
	return append(problems, stampOrTargetArgProblems(stage, ", which would rerun the tests per version or per architecture")...)
}

func (g componentStageGraph) lastStageNameProblems(want string) []string {
	if last := g.stages[len(g.stages)-1]; last.name != want {
		return []string{"the last stage is named " + strconv.Quote(last.name) + ", want `" + want + "`"}
	}
	return nil
}

func (g componentStageGraph) lastStageProblems(want string) []string {
	problems := g.lastStageNameProblems(want)
	if !g.reaches(g.stages[len(g.stages)-1], "test") {
		problems = append(problems, "the last stage takes nothing from `test` or a helper FROM test, so BuildKit prunes the tests from what it ships")
	}
	return problems
}

// TestComponentDockerfilesFollowTheStageConvention pins the repository's
// component stage convention on every component Dockerfile. Reads a sibling
// module, so it must run uncached (go test -count=1).
func TestComponentDockerfilesFollowTheStageConvention(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, component := range componentDockerfiles {
		path := filepath.Join(root, "erun-devops", "docker", component, "Dockerfile")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, problem := range componentStageConventionProblems(string(data)) {
			t.Errorf("erun-devops/docker/%s/Dockerfile: %s", component, problem)
		}
	}
}

// TestLinuxPackageDockerfilesFollowTheStageConvention pins the same convention
// on every package component, which ends in `dist` rather than `runtime`.
// Reads a sibling module, so it must run uncached (go test -count=1).
func TestLinuxPackageDockerfilesFollowTheStageConvention(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, path := range linuxPackageDockerfilePaths(t) {
		name := filepath.Base(filepath.Dir(path))
		if _, ok := linuxPackageDockerfiles[name]; !ok {
			t.Errorf("erun-devops/linux/%s/Dockerfile is not in linuxPackageDockerfiles, so the stage convention does not check it", name)
		}
	}
	for name, tested := range linuxPackageDockerfiles {
		path := filepath.Join(root, "erun-devops", "linux", name, "Dockerfile")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, problem := range packageStageConventionProblems(string(data), tested) {
			t.Errorf("erun-devops/linux/%s/Dockerfile: %s", name, problem)
		}
	}
}

// Every Dockerfile under erun-devops/docker is either a named component or a
// single-stage wrapper; a new multi-stage image has to join the list above so
// the convention covers it.
func TestEveryMultiStageDockerfileIsANamedComponent(t *testing.T) {
	named := make(map[string]struct{}, len(componentDockerfiles))
	for _, component := range componentDockerfiles {
		named[component] = struct{}{}
	}
	for _, path := range realDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		component := filepath.Base(filepath.Dir(path))
		if _, ok := named[component]; ok {
			continue
		}
		if stages := componentStagesOf(string(data)); len(stages) > 1 {
			t.Errorf("%s has %d stages but is not in componentDockerfiles, so the stage convention does not check it", path, len(stages))
		}
	}
}

// multiPartStageDockerfileContent has two parts tested side by side, joined
// by `test`.
const multiPartStageDockerfileContent = "" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS cli-build\n" +
	"RUN go build -o /out/app ./...\n" +
	"\n" +
	"FROM cli-build AS cli-test\n" +
	"RUN go test ./...\n" +
	"\n" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS scripts-test\n" +
	"RUN go test ./scripts/...\n" +
	"\n" +
	"FROM cli-test AS test\n" +
	"COPY --from=scripts-test /src/scripts /src/scripts\n" +
	"\n" +
	"FROM test AS dist\n" +
	"ARG TARGETARCH\n" +
	"RUN GOARCH=$TARGETARCH go build -o /dist/app ./...\n" +
	"\n" +
	"FROM ubuntu:24.04 AS runtime\n" +
	"COPY --from=dist /dist/app /app\n" +
	"COPY --from=test /src/scripts /scripts\n"

// testedPackageDockerfileContent is a package component that tests itself:
// build -> test -> package -> dist, the last FROM scratch.
const testedPackageDockerfileContent = "" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
	"RUN go build -o /out/app ./...\n" +
	"\n" +
	"FROM build AS test\n" +
	"RUN go test ./...\n" +
	"\n" +
	"FROM test AS package\n" +
	"ARG TARGETARCH\n" +
	"RUN GOARCH=$TARGETARCH go build -o /pkg/app ./... && dpkg-deb --build /pkg /out/app.deb\n" +
	"\n" +
	"FROM scratch AS dist\n" +
	"COPY --from=package /out/ /\n"

// untestedPackageDockerfileContent is a package component tested elsewhere.
const untestedPackageDockerfileContent = "" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
	"RUN go build -o /out/app ./...\n" +
	"\n" +
	"FROM build AS package\n" +
	"ARG TARGETARCH\n" +
	"RUN GOARCH=$TARGETARCH go build -o /pkg/app ./... && dpkg-deb --build /pkg /out/app.deb\n" +
	"\n" +
	"FROM scratch AS dist\n" +
	"COPY --from=package /out/ /\n"

func TestComponentStageConventionProblemsReportsEachBreak(t *testing.T) {
	conforming := consumedTestStageDockerfileContent
	multi := multiPartStageDockerfileContent
	cases := []struct {
		name    string
		text    string
		problem string
	}{
		{"conforming", conforming, ""},
		{"runtime copies from test directly", strings.Replace(conforming, "COPY --from=dist /dist/app", "COPY --from=test /out/app", 1), ""},
		{"standalone test without build", "FROM golang:1.26.0 AS test\nRUN go test ./...\n\nFROM scratch AS runtime\nCOPY --from=test /src/app /app\n", ""},
		{"toolchain source stage", strings.Replace(conforming, "FROM scratch AS runtime\n", "FROM alpine:3.20 AS fetcher\nRUN true\n\nFROM scratch AS runtime\nCOPY --from=fetcher /bin/tool /tool\n", 1), ""},
		{"ends at test", strings.Split(conforming, "\nFROM test AS dist")[0], "the last stage is named \"test\", want `runtime`"},
		{"multi-part", multi, ""},
		{"test not from build", strings.Replace(conforming, "FROM build AS test", "FROM golang:1.26.0 AS test", 1), "want FROM build"},
		{"last stage not runtime", strings.Replace(conforming, "AS runtime", "AS final", 1), "want `runtime`"},
		{"helper from build", strings.Replace(conforming, "FROM test AS dist", "FROM build AS dist", 1), "stage \"dist\" reads from `build`"},
		{"runtime copies from build", strings.Replace(conforming, "COPY --from=dist", "COPY --from=build", 1), "stage \"runtime\" reads from `build`"},
		{"runtime mounts from build", conforming + "RUN --mount=type=bind,from=build,target=/b true\n", "reads from `build`"},
		{"runtime never reaches test", strings.Replace(conforming, "COPY --from=dist /dist/app /app", "COPY app /app", 1), "takes nothing from `test`"},
		{"helper not from test", strings.Replace(conforming, "FROM scratch AS runtime\n", "FROM scratch AS assets\nCOPY --from=test /out/app /app\n\nFROM assets AS packed\nRUN true\n\nFROM scratch AS runtime\nCOPY --from=packed /app /app\n", 1), "stage \"packed\" is FROM assets, which does not descend from `test`"},
		{"version stamped in build", strings.Replace(conforming, "RUN go build -o /out/app", "ARG ERUN_VERSION\nRUN go build -o /out/app", 1), "stage `build` declares ARG ERUN_VERSION"},
		{"target arch in build", strings.Replace(conforming, "RUN go build -o /out/app", "ARG TARGETARCH\nRUN go build -o /out/app", 1), "stage `build` declares ARG TARGETARCH"},
		{"version stamped in test", strings.Replace(conforming, "RUN go test", "ARG ERUN_VERSION\nRUN go test", 1), "stage `test` declares ARG ERUN_VERSION"},
		{"build not pinned", strings.Replace(conforming, "--platform=$BUILDPLATFORM ", "", 1), "not pinned"},
		{"no test stage", "FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\nRUN go build ./...\n\nFROM scratch AS runtime\n", "no stage named `test`"},
		{"test stage not named test", strings.Replace(conforming, "AS test", "AS check", 1), "no stage named `test`"},
		{"part test not from its build", strings.Replace(multi, "FROM cli-build AS cli-test", "FROM golang:1.26.0 AS cli-test", 1), "stage `cli-test` is FROM golang:1.26.0, want FROM cli-build"},
		{"part build not pinned", strings.Replace(multi, "FROM --platform=$BUILDPLATFORM golang:1.26.0 AS cli-build", "FROM golang:1.26.0 AS cli-build", 1), "stage `cli-build` is not pinned"},
		{"part build without its test", strings.Replace(multi, "FROM --platform=$BUILDPLATFORM golang:1.26.0 AS scripts-test", "FROM --platform=$BUILDPLATFORM golang:1.26.0 AS docs-build\nRUN true\n\nFROM --platform=$BUILDPLATFORM golang:1.26.0 AS scripts-test", 1), "stage `docs-build` has no `docs-test`"},
		{"part build read around its test", strings.Replace(multi, "COPY --from=dist /dist/app /app", "COPY --from=cli-build /out/app /app", 1), "stage \"runtime\" reads from `cli-build`; only `cli-test` builds on it"},
		{"part test not joined into test", strings.Replace(multi, "COPY --from=scripts-test /src/scripts /src/scripts\n", "", 1), "stage `scripts-test` does not feed `test`"},
		{"runtime copies around test from a part test", strings.Replace(strings.Replace(multi, "COPY --from=scripts-test /src/scripts /src/scripts\n", "", 1), "COPY --from=test /src/scripts", "COPY --from=scripts-test /src/scripts", 1), "stage `scripts-test` does not feed `test`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := componentStageConventionProblems(tc.text)
			if tc.problem == "" {
				if len(problems) != 0 {
					t.Fatalf("want no problems, got %q", problems)
				}
				return
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.problem) {
				t.Fatalf("want a problem containing %q, got %q", tc.problem, problems)
			}
		})
	}
}

func TestPackageStageConventionProblemsReportsEachBreak(t *testing.T) {
	tested := testedPackageDockerfileContent
	untested := untestedPackageDockerfileContent
	cases := []struct {
		name    string
		text    string
		tested  bool
		problem string
	}{
		{"tested package", tested, true, ""},
		{"untested package", untested, false, ""},
		{"tested package without its test stage", untested, true, "no stage named `test`"},
		{"last stage not dist", strings.Replace(tested, "AS dist", "AS runtime", 1), true, "want `dist`"},
		{"untested last stage not dist", strings.Replace(untested, "AS dist", "AS out", 1), false, "want `dist`"},
		{"dist not from scratch", strings.Replace(tested, "FROM scratch AS dist", "FROM debian:13 AS dist", 1), true, "want FROM scratch"},
		{"package from build around test", strings.Replace(tested, "FROM test AS package", "FROM build AS package", 1), true, "stage \"package\" reads from `build`"},
		{"dist never reaches test", strings.Replace(tested, "COPY --from=package /out/ /", "COPY app.deb /", 1), true, "takes nothing from `test`"},
		{"untested build stamps the version", strings.Replace(untested, "RUN go build -o /out/app", "ARG ERUN_VERSION\nRUN go build -o /out/app", 1), false, "stage `build` declares ARG ERUN_VERSION"},
		{"untested build not pinned", strings.Replace(untested, "--platform=$BUILDPLATFORM ", "", 1), false, "not pinned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := packageStageConventionProblems(tc.text, tc.tested)
			if tc.problem == "" {
				if len(problems) != 0 {
					t.Fatalf("want no problems, got %q", problems)
				}
				return
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.problem) {
				t.Fatalf("want a problem containing %q, got %q", tc.problem, problems)
			}
		})
	}
}

// The cli-test stage of the erun-devops Dockerfile copies erun-devops/docker
// one image directory at a time, so a change to a script the runtime image
// bakes does not rerun the Go suites, and erun-devops/linux whole. A directory
// left off would vanish from the suites' view inside the build -- the
// Dockerfile contract tests above would silently stop checking it -- while
// every host run still sees it, so the list is checked from here.
func TestCLITestStageCopiesEveryDockerDirectory(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read the erun-devops Dockerfile: %v", err)
	}
	text := string(data)
	for _, path := range realDockerfilePaths(t) {
		image := filepath.Base(filepath.Dir(path))
		if image == "erun-devops" {
			continue
		}
		copyLine := "COPY erun-devops/docker/" + image + " /src/erun-devops/docker/" + image + "\n"
		if !strings.Contains(text, copyLine) {
			t.Errorf("erun-devops/docker/erun-devops/Dockerfile: the cli-test stage lacks %q, so the suites it runs never see that image's Dockerfile", strings.TrimSpace(copyLine))
		}
	}
	if packageLine := "COPY erun-devops/linux /src/erun-devops/linux\n"; !strings.Contains(text, packageLine) {
		t.Errorf("erun-devops/docker/erun-devops/Dockerfile: the cli-test stage lacks %q, so the suites it runs never see the package Dockerfiles", strings.TrimSpace(packageLine))
	}
}
