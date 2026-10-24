package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
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
	if err := os.MkdirAll(filepath.Join(projectRoot, "team-devops", "linux", "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "team-devops", "linux", "legacy", "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return projectRoot, uiDir, cliDir
}

// The build side is the integration golden build/dry_run_linux_package_from_component_dir.
func TestPushFromALinuxPackageDirectoryIsRefusedByName(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, uiDir)

	_, _, err := ResolveDockerPushSpec(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil || !strings.Contains(err.Error(), "is a linux package, not an image") {
		t.Fatalf("a push from a package directory must be refused by name, got %v", err)
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
