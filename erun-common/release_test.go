package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveReleaseSpecStableRelease(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if spec.Mode != ReleaseModeStable || spec.Version != "1.4.2" || spec.NextVersion != "1.4.3" {
		t.Fatalf("unexpected release spec: %+v", spec)
	}
	if spec.ReleaseRoot != filepath.Join(projectRoot, "erun-devops") {
		t.Fatalf("unexpected release root: %q", spec.ReleaseRoot)
	}
	if len(spec.Charts) != 1 || spec.Charts[0].Version != "1.4.2" || spec.Charts[0].AppVersion != "1.4.2" {
		t.Fatalf("unexpected charts: %+v", spec.Charts)
	}
	if len(spec.DockerImages) != 1 || spec.DockerImages[0].Tag != "erunpaas/api:1.4.2" {
		t.Fatalf("unexpected docker images: %+v", spec.DockerImages)
	}
	if len(spec.Stages) != 5 {
		t.Fatalf("expected 5 stages, got %+v", spec.Stages)
	}
	if got := spec.Stages[0].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"fetch", "origin"}) {
		t.Fatalf("unexpected sync fetch command: %+v", got)
	}
	if got := spec.Stages[0].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"rebase", "origin/main"}) {
		t.Fatalf("unexpected sync rebase command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")}) {
		t.Fatalf("unexpected release add command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"commit", "-m", "[skip ci] release 1.4.2"}) {
		t.Fatalf("unexpected release commit command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"tag", "-a", "v1.4.2", "-m", "Release 1.4.2"}) {
		t.Fatalf("unexpected release tag command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"add", filepath.Join("erun-devops", "VERSION")}) {
		t.Fatalf("unexpected bump add command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"checkout", "develop"}) {
		t.Fatalf("unexpected sync checkout command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"merge", "--no-edit", "-X", "theirs", "main"}) {
		t.Fatalf("unexpected sync merge command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"checkout", "main"}) {
		t.Fatalf("unexpected sync return command: %+v", got)
	}
	if got := spec.Stages[4].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "main", "develop"}) {
		t.Fatalf("unexpected push command: %+v", got)
	}
}

func TestResolveReleaseSpecSkipsLinuxScriptsWhenUnsupported(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := hostLookPath
	currentGOOS = func() string { return "darwin" }
	hostLookPath = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		hostLookPath = prevLookPath
	})

	projectRoot := setupReleaseProjectGitRepo(t, "main")
	linuxDir := filepath.Join(projectRoot, "erun-devops", "linux", "erun-cli")
	if err := os.MkdirAll(linuxDir, 0o755); err != nil {
		t.Fatalf("mkdir linux dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write release.sh: %v", err)
	}

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}
	if len(spec.LinuxReleases) != 0 || !spec.SkippedLinux {
		t.Fatalf("expected linux releases to be skipped, got %+v", spec)
	}
}

func TestResolveReleaseSpecStableReleaseIncludesPackagingUpdatesWhenPresent(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{WithPackaging: true})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if len(spec.Stages) != 7 {
		t.Fatalf("expected 7 stages, got %+v", spec.Stages)
	}
	if len(spec.Stages[1].FileUpdates) != 3 {
		t.Fatalf("expected chart, formula, and scoop updates, got %+v", spec.Stages[1].FileUpdates)
	}
	if got := spec.Stages[1].GitCommands[0].Args; !reflect.DeepEqual(got, []string{
		"add",
		filepath.Join("erun-devops", "k8s", "api", "Chart.yaml"),
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}) {
		t.Fatalf("unexpected release add command: %+v", got)
	}
	if formula := spec.Stages[1].FileUpdates[1].Content; !strings.Contains(formula, `url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"`) {
		t.Fatalf("unexpected formula update: %s", formula)
	}
	if scoop := spec.Stages[1].FileUpdates[2].Content; !strings.Contains(scoop, `"version": "1.4.2"`) || !strings.Contains(scoop, `"extract_dir": "erun-1.4.2"`) {
		t.Fatalf("unexpected scoop update: %s", scoop)
	}
	if spec.Stages[2].Name != "push-release-tag" {
		t.Fatalf("unexpected tag push stage: %+v", spec.Stages[2])
	}
	if got := spec.Stages[2].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "origin", "v1.4.2"}) {
		t.Fatalf("unexpected tag push command: %+v", got)
	}
	if spec.Stages[3].Name != "sync-packaging-checksums" || spec.Stages[3].PackagingSync == nil {
		t.Fatalf("unexpected packaging sync stage: %+v", spec.Stages[3])
	}
	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{
		"add",
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}) {
		t.Fatalf("unexpected packaging add command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"commit", "-m", "[skip ci] sync package metadata 1.4.2"}) {
		t.Fatalf("unexpected packaging commit command: %+v", got)
	}
}

func TestResolveReleaseSpecUsesConfiguredBranches(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{
		ProjectConfig: ProjectConfig{
			Release: ReleaseConfig{
				MainBranch:    "trunk",
				DevelopBranch: "integration",
			},
		},
	})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "integration", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if spec.Mode != ReleaseModeCandidate || spec.Version != "1.4.2-rc.abc1234" || spec.NextVersion != "" {
		t.Fatalf("unexpected release spec: %+v", spec)
	}
	if len(spec.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %+v", spec.Stages)
	}
	if got := spec.Stages[0].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"fetch", "origin"}) {
		t.Fatalf("unexpected candidate sync fetch command: %+v", got)
	}
	if got := spec.Stages[0].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"rebase", "origin/integration"}) {
		t.Fatalf("unexpected candidate sync rebase command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234"}) {
		t.Fatalf("unexpected candidate tag command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "integration"}) {
		t.Fatalf("unexpected candidate push command: %+v", got)
	}
}

func TestRunReleaseSpecWritesFilesAndRunsGitStages(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "develop")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	var gitCalls [][]string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, append([]string{dir}, args...))
		return nil
	}, nil); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	versionData, err := os.ReadFile(filepath.Join(projectRoot, "erun-devops", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got := string(versionData); got != "1.4.3\n" {
		t.Fatalf("unexpected VERSION content: %q", got)
	}

	chartData, err := os.ReadFile(filepath.Join(projectRoot, "erun-devops", "k8s", "api", "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	if !bytes.Contains(chartData, []byte("version: 1.4.2")) || !bytes.Contains(chartData, []byte("appVersion: 1.4.2")) {
		t.Fatalf("unexpected chart content: %s", chartData)
	}

	wantCalls := [][]string{
		{projectRoot, "fetch", "origin"},
		{projectRoot, "rebase", "origin/main"},
		{projectRoot, "add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")},
		{projectRoot, "commit", "-m", "[skip ci] release 1.4.2"},
		{projectRoot, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2"},
		{projectRoot, "add", filepath.Join("erun-devops", "VERSION")},
		{projectRoot, "commit", "-m", "[skip ci] prepare 1.4.3"},
		{projectRoot, "checkout", "develop"},
		{projectRoot, "merge", "--no-edit", "-X", "theirs", "main"},
		{projectRoot, "checkout", "main"},
		{projectRoot, "push", "--follow-tags", "origin", "main", "develop"},
	}
	if !reflect.DeepEqual(gitCalls, wantCalls) {
		t.Fatalf("unexpected git calls: got %+v want %+v", gitCalls, wantCalls)
	}
}

func TestRunReleaseSpecRewritesStablePackagingMetadataWhenPresent(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepoWithOptions(t, "main", releaseProjectOptions{WithPackaging: true})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	var gitCalls [][]string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := runReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, append([]string{dir}, args...))
		return nil
	}, nil, func(Context, ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error) {
		return []ReleaseFileUpdate{
			{
				Path: filepath.Join(projectRoot, "Formula", "erun.rb"),
				Content: `class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"
  sha256 "formula-checksum"
  license "MIT"
end
`,
			},
			{
				Path: filepath.Join(projectRoot, "bucket", "erun.json"),
				Content: `{
  "version": "1.4.2",
  "description": "Multi-tenant multi-environment deployment and management tool",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": [
    "go"
  ],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.zip",
  "hash": "scoop-checksum",
  "extract_dir": "erun-1.4.2",
  "installer": {
    "script": [
      "go build"
    ]
  },
  "bin": [
    "erun.exe",
    "emcp.exe"
  ]
}
`,
			},
		}, nil
	}); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	formulaData, err := os.ReadFile(filepath.Join(projectRoot, "Formula", "erun.rb"))
	if err != nil {
		t.Fatalf("read formula: %v", err)
	}
	if !strings.Contains(string(formulaData), `url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"`) || !strings.Contains(string(formulaData), `sha256 "formula-checksum"`) {
		t.Fatalf("unexpected formula content: %s", formulaData)
	}

	scoopData, err := os.ReadFile(filepath.Join(projectRoot, "bucket", "erun.json"))
	if err != nil {
		t.Fatalf("read scoop manifest: %v", err)
	}
	scoop := string(scoopData)
	if !strings.Contains(scoop, `"version": "1.4.2"`) || !strings.Contains(scoop, `"url": "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.zip"`) || !strings.Contains(scoop, `"extract_dir": "erun-1.4.2"`) || !strings.Contains(scoop, `"hash": "scoop-checksum"`) {
		t.Fatalf("unexpected scoop content: %s", scoop)
	}

	if got := gitCalls[5]; !reflect.DeepEqual(got, []string{
		projectRoot,
		"push",
		"origin",
		"v1.4.2",
	}) {
		t.Fatalf("unexpected tag push call: %+v", got)
	}
	if got := gitCalls[6]; !reflect.DeepEqual(got, []string{
		projectRoot,
		"add",
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}) {
		t.Fatalf("unexpected packaging add call: %+v", got)
	}
	if got := gitCalls[7]; !reflect.DeepEqual(got, []string{
		projectRoot,
		"commit",
		"-m",
		"[skip ci] sync package metadata 1.4.2",
	}) {
		t.Fatalf("unexpected packaging commit call: %+v", got)
	}
}

func TestRunReleaseSpecSkipsPackagingCommitWhenChecksumsAreAlreadyCurrent(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepoWithOptions(t, "main", releaseProjectOptions{WithPackaging: true})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	var gitCalls [][]string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := runReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, append([]string{dir}, args...))
		return nil
	}, nil, func(Context, ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	for _, call := range gitCalls {
		if len(call) >= 4 && call[1] == "commit" && call[3] == "[skip ci] sync package metadata 1.4.2" {
			t.Fatalf("did not expect packaging commit when checksum updates are empty: %+v", call)
		}
	}
	if got := gitCalls[6]; !reflect.DeepEqual(got, []string{
		projectRoot,
		"add",
		filepath.Join("erun-devops", "VERSION"),
	}) {
		t.Fatalf("expected version bump to follow tag push when checksum updates are empty, got %+v", got)
	}
}

func TestUpdateHomebrewFormulaReleaseChecksum(t *testing.T) {
	projectRoot := t.TempDir()
	formulaPath := filepath.Join(projectRoot, "Formula", "erun.rb")
	if err := os.MkdirAll(filepath.Dir(formulaPath), 0o755); err != nil {
		t.Fatalf("mkdir formula dir: %v", err)
	}
	if err := os.WriteFile(formulaPath, []byte(`class Erun < Formula
  url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"
  sha256 "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
end
`), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}

	content, changed, err := updateHomebrewFormulaReleaseChecksum(formulaPath, "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	if err != nil {
		t.Fatalf("updateHomebrewFormulaReleaseChecksum failed: %v", err)
	}
	if !changed || !strings.Contains(content, `sha256 "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"`) {
		t.Fatalf("unexpected checksum update: changed=%v content=%s", changed, content)
	}
}

func TestUpdateScoopManifestReleaseChecksum(t *testing.T) {
	projectRoot := t.TempDir()
	manifestPath := filepath.Join(projectRoot, "bucket", "erun.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir bucket dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{
  "version": "1.4.2",
  "description": "Multi-tenant multi-environment deployment and management tool",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": [
    "go"
  ],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.zip",
  "hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "extract_dir": "erun-1.4.2",
  "installer": {
    "script": [
      "go build"
    ]
  },
  "bin": [
    "erun.exe",
    "emcp.exe"
  ]
}
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	content, changed, err := updateScoopManifestReleaseChecksum(manifestPath, "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	if err != nil {
		t.Fatalf("updateScoopManifestReleaseChecksum failed: %v", err)
	}
	if !changed || !strings.Contains(content, `"hash": "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"`) {
		t.Fatalf("unexpected checksum update: changed=%v content=%s", changed, content)
	}
}

func TestRunReleaseSpecCandidateRunsTagAndPush(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "develop", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	var gitCalls [][]string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, append([]string{dir}, args...))
		return nil
	}, nil); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	wantCalls := [][]string{
		{projectRoot, "fetch", "origin"},
		{projectRoot, "rebase", "origin/develop"},
		{projectRoot, "add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")},
		{projectRoot, "commit", "-m", "[skip ci] release 1.4.2-rc.abc1234"},
		{projectRoot, "tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234"},
		{projectRoot, "push", "--follow-tags", "origin", "develop"},
	}
	if !reflect.DeepEqual(gitCalls, wantCalls) {
		t.Fatalf("unexpected git calls: got %+v want %+v", gitCalls, wantCalls)
	}
}

func TestRunReleaseSpecCandidateSkipsExistingTagAtHead(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "k8s", "api", "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 1.4.2-rc.abc1234\nappVersion: 1.4.2-rc.abc1234\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "init", "-b", "develop")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "add", ".")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "commit", "-m", "initial")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "develop", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	var gitCalls [][]string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, append([]string{dir}, args...))
		return nil
	}, nil); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	wantCalls := [][]string{
		{projectRoot, "fetch", "origin"},
		{projectRoot, "rebase", "origin/develop"},
		{projectRoot, "push", "--follow-tags", "origin", "develop"},
	}
	if !reflect.DeepEqual(gitCalls, wantCalls) {
		t.Fatalf("unexpected git calls: got %+v want %+v", gitCalls, wantCalls)
	}
}

func TestRunReleaseSpecReturnsErrorWhenExistingTagPointsElsewhere(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{})
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "k8s", "api", "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 1.4.2-rc.abc1234\nappVersion: 1.4.2-rc.abc1234\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "init", "-b", "develop")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "add", ".")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "commit", "-m", "initial")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234")
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "README.tmp"), []byte("change\n"), 0o644); err != nil {
		t.Fatalf("write temp change: %v", err)
	}
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "add", "erun-devops/README.tmp")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "commit", "-m", "advance head")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "develop", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err = RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		return nil
	}, nil)
	if err == nil {
		t.Fatal("expected existing tag mismatch error")
	}
	if !strings.Contains(err.Error(), `release tag "v1.4.2-rc.abc1234" already exists`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunReleaseSpecForceDeletesExistingTagAndRecreatesIt(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "develop")
	remoteBase := t.TempDir()
	remoteRoot := filepath.Join(remoteBase, "origin.git")
	runGitWithEnv(t, remoteBase, nil, "init", "--bare", remoteRoot)
	runGitWithEnv(t, projectRoot, nil, "remote", "add", "origin", remoteRoot)
	runGitWithEnv(t, projectRoot, nil, "push", "-u", "origin", "develop")
	runGitWithEnv(t, projectRoot, nil, "tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234")
	runGitWithEnv(t, projectRoot, nil, "push", "origin", "v1.4.2-rc.abc1234")

	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "README.tmp"), []byte("change\n"), 0o644); err != nil {
		t.Fatalf("write temp change: %v", err)
	}
	runGitWithEnv(t, projectRoot, nil, "add", "erun-devops/README.tmp")
	runGitWithEnv(t, projectRoot, nil, "commit", "-m", "advance head")
	runGitWithEnv(t, projectRoot, nil, "push", "origin", "develop")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "develop", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{Force: true},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunReleaseSpec(ctx, spec, nil, nil); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	headCommit := strings.TrimSpace(runGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	localTagCommit := strings.TrimSpace(runGitOutput(t, projectRoot, "rev-parse", "v1.4.2-rc.abc1234^{}"))
	if localTagCommit != headCommit {
		t.Fatalf("expected local tag at HEAD, got tag=%s head=%s", localTagCommit, headCommit)
	}

	remoteTagOutput := strings.Fields(runGitOutput(t, projectRoot, "ls-remote", "--tags", "origin", "refs/tags/v1.4.2-rc.abc1234^{}"))
	if len(remoteTagOutput) == 0 {
		t.Fatal("expected remote tag output")
	}
	if remoteTagOutput[0] != headCommit {
		t.Fatalf("expected remote tag at HEAD, got tag=%s head=%s", remoteTagOutput[0], headCommit)
	}
}

func TestRunReleaseSpecReturnsErrorWhenWorktreeIsDirty(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectRoot, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	ctx := Context{
		Logger: NewLoggerWithWriters(2, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err = RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
		t.Fatalf("did not expect git command %v", args)
		return nil
	}, nil)
	if err == nil {
		t.Fatal("expected dirty worktree error")
	}
	if err.Error() != "release requires a clean git worktree; commit or stash changes first" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveReleaseSpecStableReleaseUsesConfiguredDevelopBranchForSyncAndPush(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{
		ProjectConfig: ProjectConfig{
			Release: ReleaseConfig{
				MainBranch:    "trunk",
				DevelopBranch: "integration",
			},
		},
	})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "trunk", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return true, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"checkout", "integration"}) {
		t.Fatalf("unexpected configured sync checkout command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"merge", "--no-edit", "-X", "theirs", "trunk"}) {
		t.Fatalf("unexpected configured sync merge command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"checkout", "trunk"}) {
		t.Fatalf("unexpected configured sync return command: %+v", got)
	}
	if got := spec.Stages[4].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "trunk", "integration"}) {
		t.Fatalf("unexpected configured push command: %+v", got)
	}
}

func TestResolveReleaseSpecStableReleaseSkipsDevelopSyncWhenBranchMissing(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
		func(string, string) (bool, error) { return false, nil },
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if len(spec.Stages) != 4 {
		t.Fatalf("expected 4 stages without develop sync, got %+v", spec.Stages)
	}
	if spec.Stages[3].Name != "push" {
		t.Fatalf("unexpected final stage: %+v", spec.Stages[3])
	}
	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "main"}) {
		t.Fatalf("unexpected main-only push command: %+v", got)
	}
}

func TestGitCommandRunnerUsesFallbackIdentityWhenGitConfigIsMissing(t *testing.T) {
	projectRoot := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "init", "-b", "main")

	if err := os.WriteFile(filepath.Join(projectRoot, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "add", "README.md")
	runGitWithEnv(t, projectRoot, []string{
		"GIT_AUTHOR_NAME=Codex",
		"GIT_AUTHOR_EMAIL=codex@example.com",
		"GIT_COMMITTER_NAME=Codex",
		"GIT_COMMITTER_EMAIL=codex@example.com",
	}, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(projectRoot, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update README: %v", err)
	}

	if err := GitCommandRunner(projectRoot, io.Discard, io.Discard, "add", "README.md"); err != nil {
		t.Fatalf("GitCommandRunner add failed: %v", err)
	}
	if err := GitCommandRunner(projectRoot, io.Discard, io.Discard, "commit", "-m", "update"); err != nil {
		t.Fatalf("GitCommandRunner commit failed: %v", err)
	}
	if err := GitCommandRunner(projectRoot, io.Discard, io.Discard, "tag", "-a", "v1.0.0", "-m", "Release 1.0.0"); err != nil {
		t.Fatalf("GitCommandRunner tag failed: %v", err)
	}

	author := strings.TrimSpace(runGitOutput(t, projectRoot, "log", "-1", "--format=%an <%ae>"))
	if author != defaultReleaseGitUserName+" <"+defaultReleaseGitUserEmail+">" {
		t.Fatalf("unexpected author identity: %q", author)
	}

	tagger := strings.TrimSpace(runGitOutput(t, projectRoot, "for-each-ref", "refs/tags/v1.0.0", "--format=%(taggername) %(taggeremail)"))
	if tagger != defaultReleaseGitUserName+" <"+defaultReleaseGitUserEmail+">" {
		t.Fatalf("unexpected tagger identity: %q", tagger)
	}
}

type releaseProjectOptions struct {
	ProjectConfig ProjectConfig
	WithPackaging bool
}

func setupReleaseProject(t *testing.T, options releaseProjectOptions) string {
	t.Helper()

	projectRoot := t.TempDir()
	releaseRoot := filepath.Join(projectRoot, "erun-devops")
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		t.Fatalf("mkdir release root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	chartPath := filepath.Join(releaseRoot, "k8s", "api")
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	dockerPath := filepath.Join(releaseRoot, "docker", "api")
	if err := os.MkdirAll(dockerPath, 0o755); err != nil {
		t.Fatalf("mkdir docker path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dockerPath, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	otherDockerPath := filepath.Join(releaseRoot, "docker", "base")
	if err := os.MkdirAll(otherDockerPath, 0o755); err != nil {
		t.Fatalf("mkdir other docker path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDockerPath, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write other Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDockerPath, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write other VERSION: %v", err)
	}

	if err := SaveProjectConfig(projectRoot, options.ProjectConfig); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}
	if options.WithPackaging {
		formulaPath := filepath.Join(projectRoot, "Formula")
		if err := os.MkdirAll(formulaPath, 0o755); err != nil {
			t.Fatalf("mkdir Formula: %v", err)
		}
		if err := os.WriteFile(filepath.Join(formulaPath, "erun.rb"), []byte(`class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.4.1.tar.gz"
  sha256 "unchanged"
  license "MIT"
end
`), 0o644); err != nil {
			t.Fatalf("write formula: %v", err)
		}

		bucketPath := filepath.Join(projectRoot, "bucket")
		if err := os.MkdirAll(bucketPath, 0o755); err != nil {
			t.Fatalf("mkdir bucket: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bucketPath, "erun.json"), []byte(`{
  "version": "1.4.1",
  "description": "Multi-tenant multi-environment deployment and management tool",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": [
    "go"
  ],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.4.1.zip",
  "hash": "unchanged",
  "extract_dir": "erun-1.4.1",
  "installer": {
    "script": [
      "go build"
    ]
  },
  "bin": [
    "erun.exe",
    "emcp.exe"
  ]
}
`), 0o644); err != nil {
			t.Fatalf("write scoop manifest: %v", err)
		}
	}

	return projectRoot
}

func setupReleaseProjectGitRepoWithOptions(t *testing.T, branch string, options releaseProjectOptions) string {
	t.Helper()

	projectRoot := setupReleaseProject(t, options)
	runGitWithEnv(t, projectRoot, nil, "init", "-b", branch)
	runGitWithEnv(t, projectRoot, nil, "config", "user.email", "codex@example.com")
	runGitWithEnv(t, projectRoot, nil, "config", "user.name", "Codex")
	runGitWithEnv(t, projectRoot, nil, "add", ".")
	runGitWithEnv(t, projectRoot, nil, "commit", "-m", "initial")
	return projectRoot
}

func runGitWithEnv(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}
