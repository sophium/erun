package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (
	testedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun-app ./...\n" +
		"\n" +
		"FROM build AS test\n" +
		"RUN go test ./...\n" +
		"\n" +
		"FROM test AS package\n" +
		"ARG ERUN_VERSION\n" +
		"RUN dpkg-deb --build /pkg /erun-desktop_${ERUN_VERSION}.deb\n" +
		"\n" +
		"FROM scratch AS dist\n" +
		"COPY --from=package /*.deb /\n"
	untestedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun ./...\n" +
		"\n" +
		"FROM build AS dist\n" +
		"RUN dpkg-deb --build /pkg /erun.deb\n"
)

// linuxPackageProject lays out a <tenant>-devops tree with one image under
// docker/ and two packages under linux/: one with tests, one without.
func linuxPackageProject(t *testing.T) (projectRoot, uiDir, cliDir string) {
	t.Helper()
	projectRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestDockerfile(t, filepath.Join(projectRoot, "team-devops", "docker", "api"),
		"FROM alpine:3.22 AS build\nRUN true\n\nFROM build AS test\nRUN true\n\nFROM alpine:3.22 AS runtime\nCOPY --from=test /etc/hostname /hostname\n")
	uiDir = filepath.Join(projectRoot, "team-devops", "linux", "erun-ui")
	writeTestDockerfile(t, uiDir, testedLinuxPackageDockerfile)
	cliDir = filepath.Join(projectRoot, "team-devops", "linux", "erun-cli")
	writeTestDockerfile(t, cliDir, untestedLinuxPackageDockerfile)
	// A leftover script-based package directory is not a package.
	if err := os.MkdirAll(filepath.Join(projectRoot, "team-devops", "linux", "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "team-devops", "linux", "legacy", "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return projectRoot, uiDir, cliDir
}

func linuxPackageNames(packages []shared.LinuxPackageSpec) []string {
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Name)
	}
	return names
}

// A package directory holds a Dockerfile, but it is never an image: a build
// from it resolves no docker build, and a push from it is refused by name.
func TestLinuxPackageDockerfileIsNeverAnImage(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, uiDir)

	if contexts, err := build.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, boundaryTarget(projectRoot)); !errors.Is(err, shared.ErrDockerBuildContextNotFound) {
		t.Fatalf("a package directory resolved image contexts %+v, %v", contexts, err)
	}
	execution, err := buildrun.ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.DockerBuilds) != 0 || !slices.Equal(linuxPackageNames(execution.LinuxPackages), []string{"erun-ui"}) {
		t.Fatalf("expected only the erun-ui package, got images %+v and packages %+v", execution.DockerBuilds, execution.LinuxPackages)
	}
	_, _, err = buildrun.ResolveDockerPushSpec(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil || !strings.Contains(err.Error(), "is a linux package, not an image") {
		t.Fatalf("a push from a package directory must be refused by name, got %v", err)
	}
}

// From the project root a plain build tests the packages beside the images,
// each with the project root as its context.
func TestPlainBuildResolvesLinuxPackagesBesideImages(t *testing.T) {
	projectRoot, _, _ := linuxPackageProject(t)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)
	execution, err := buildrun.ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.DockerBuilds) != 1 || len(execution.LinuxPackages) != 2 {
		t.Fatalf("expected one image and two packages, got %+v and %+v", execution.DockerBuilds, execution.LinuxPackages)
	}
	for _, pkg := range execution.LinuxPackages {
		if pkg.ContextDir != projectRoot {
			t.Fatalf("package %s builds from %s, want the project root %s", pkg.Name, pkg.ContextDir, projectRoot)
		}
	}
	if result := buildrun.NewBuildResult(execution); !slices.Equal(result.Packages, []string{"erun-cli", "erun-ui"}) {
		t.Fatalf("build result packages = %v", result.Packages)
	}
}

func TestPlainBuildRefusesAPackageWithNothingToRun(t *testing.T) {
	projectRoot, _, cliDir := linuxPackageProject(t)
	writeTestDockerfile(t, cliDir, "FROM debian:bookworm AS dist\nRUN true\n")
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)
	_, err := buildrun.ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil || !strings.Contains(err.Error(), "linux package erun-cli declares neither a `test` nor a `build` stage") {
		t.Fatalf("expected the package without a test or build stage to be refused, got %v", err)
	}
}

func fakeDockerRecorder(t *testing.T, exitCode string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	stub := writeExecutableScript(t, strings.Join([]string{
		`echo "$*" >> '` + logPath + `'`,
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    type=local,dest=*) dest="${arg#type=local,dest=}"; mkdir -p "$dest/usr/share"; : > "$dest/pkg_$(basename "$dest").deb"; : > "$dest/usr/share/cached.deb" ;;`,
		`  esac`,
		`done`,
		`exit ` + exitCode,
	}, "\n"))
	t.Setenv("ERUN_DOCKER_BIN", stub)
	return logPath
}
