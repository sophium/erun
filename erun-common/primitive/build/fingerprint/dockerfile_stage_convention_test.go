package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// The backend tests run only if the `test` stage runs them and a later stage consumes it.
// The component is named rather than iterated: deleting the stage would leave nothing to check.
const componentTestCommand = "go test -count=1 ./..."

// Delimited by its own FROM, so a rename reads as "no such stage" rather than a neighbour's body.
func testStageInstructionLines(text string) []string {
	var lines []string
	inTestStage := false
	for _, line := range strings.Split(text, "\n") {
		if dockerfileFromRefPattern.MatchString(line) {
			inTestStage = dockerspec.DockerfileTestStagePattern.MatchString(line)
			continue
		}
		if inTestStage {
			lines = append(lines, line)
		}
	}
	return lines
}

// Without a consumed test stage, the opt-in suites skip and the gate still reports `ok`.
func TestErunBackendAPIOptInE2ESuitesStayInAConsumedTestStage(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	// Literal Join parts: the repo-root read scan cannot resolve a computed path.
	path := filepath.Join(root, "erun-devops", "docker", "erun-backend-api", "Dockerfile")
	venue := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !dockerfileStageRuns(dockerfileStage{lines: testStageInstructionLines(string(data))}, componentTestCommand) {
		t.Errorf("%s declares no `test` stage running %s — the component's tests, its database suites included, would never run before its image is built", venue, componentTestCommand)
	}
	if !dockerspec.DockerfileHasConsumedTestStage(path) {
		t.Errorf("%s no longer declares a `test` stage a later stage consumes — without the `AS test` stage and a later stage built or copying from it the image builds and publishes with its tests never run, and a fingerprint written after that build would let later builds reuse an untested result", venue)
	}
}

// Comment lines are skipped: a prose mention is not a stage running the command.
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

// Named rather than discovered: a rule over multi-stage files goes quiet when one collapses.
var componentDockerfiles = []string{
	"erun-backend-api",
	"erun-backend-db",
	"erun-console",
	"erun-devops",
	"erun-dns01-webhook",
	"erun-docs",
}

// erun-cli's CLI and MCP server are tested in the erun-devops cli-test stage instead.
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

func (s componentStage) stageReferences() []string {
	var refs []string
	for _, line := range s.lines {
		for _, m := range dockerfileStageReferenceArg.FindAllStringSubmatch(line, -1) {
			refs = append(refs, strings.ToLower(m[1]))
		}
	}
	return refs
}

// componentStageConventionProblems checks build -> test -> helpers -> runtime: build stages pin
// BUILDPLATFORM and stamp nothing, only the test stage reads them, and runtime reaches `test`.
func componentStageConventionProblems(text string) []string {
	return stageConventionProblems(text, "runtime", true)
}

// packageStageConventionProblems checks the same chain ending in a FROM scratch `dist`.
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

// stageRole splits `build` into ("", "build") and `cli-test` into ("cli", "test").
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

func (g componentStageGraph) reads(stage componentStage) []string {
	var local []string
	for _, name := range append([]string{stage.base}, stage.stageReferences()...) {
		if _, ok := g.byName[name]; ok {
			local = append(local, name)
		}
	}
	return local
}

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

// buildReadProblems reports a stage that reads a build stage it does not test.
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

// Reads a sibling module, so it must run uncached (go test -count=1).
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

// A directory missing from cli-test's COPY list silently drops out of the Dockerfile contract tests.
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
