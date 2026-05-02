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
	requireNoError(t, err, "resolveReleaseSpec failed")

	requireStableReleaseSpec(t, spec, projectRoot)
}

func requireStableReleaseSpec(t *testing.T, spec ReleaseSpec, projectRoot string) {
	t.Helper()
	requireCondition(t, spec.Mode == ReleaseModeStable && spec.Version == "1.4.2" && spec.NextVersion == "1.4.3", "unexpected release spec: %+v", spec)
	requireEqual(t, spec.ReleaseRoot, filepath.Join(projectRoot, "erun-devops"), "unexpected release root")
	requireCondition(t, len(spec.Charts) == 1 && spec.Charts[0].Version == "1.4.2" && spec.Charts[0].AppVersion == "1.4.2", "unexpected charts: %+v", spec.Charts)
	requireCondition(t, len(spec.DockerImages) == 1 && spec.DockerImages[0].Tag == "erunpaas/api:1.4.2", "unexpected docker images: %+v", spec.DockerImages)
	requireEqual(t, len(spec.Stages), 5, "stage count")
	requireStableReleaseStageCommands(t, spec, projectRoot)
}

func requireStableReleaseStageCommands(t *testing.T, spec ReleaseSpec, projectRoot string) {
	t.Helper()
	requireDeepEqual(t, spec.Stages[0].GitCommands[0].Args, []string{"fetch", "origin"}, "unexpected sync fetch command")
	requireDeepEqual(t, spec.Stages[0].GitCommands[1].Args, []string{"rebase", "origin/main"}, "unexpected sync rebase command")
	requireDeepEqual(t, spec.Stages[1].GitCommands[0].Args, []string{"add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")}, "unexpected release add command")
	requireDeepEqual(t, spec.Stages[1].GitCommands[1].Args, []string{"commit", "-m", "[skip ci] release 1.4.2"}, "unexpected release commit command")
	requireDeepEqual(t, spec.Stages[1].GitCommands[2].Args, []string{"tag", "-a", "v1.4.2", "-m", "Release 1.4.2"}, "unexpected release tag command")
	requireDeepEqual(t, spec.Stages[2].GitCommands[0].Args, []string{"add", filepath.Join("erun-devops", "VERSION")}, "unexpected bump add command")
	requireDeepEqual(t, spec.Stages[3].GitCommands[0].Args, []string{"checkout", "develop"}, "unexpected sync checkout command")
	requireDeepEqual(t, spec.Stages[3].GitCommands[1].Args, []string{"merge", "--no-edit", "-X", "theirs", "main"}, "unexpected sync merge command")
	requireDeepEqual(t, spec.Stages[3].GitCommands[2].Args, []string{"checkout", "main"}, "unexpected sync return command")
	requireDeepEqual(t, spec.Stages[4].GitCommands[0].Args, []string{"push", "--follow-tags", "origin", "main", "develop"}, "unexpected push command")
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

	requireStablePackagingReleaseSpec(t, spec)
}

func requireStablePackagingReleaseSpec(t *testing.T, spec ReleaseSpec) {
	t.Helper()
	requireEqual(t, len(spec.Stages), 7, "stage count")
	requireEqual(t, len(spec.Stages[1].FileUpdates), 3, "release file update count")
	requireDeepEqual(t, spec.Stages[1].GitCommands[0].Args, []string{
		"add",
		filepath.Join("erun-devops", "k8s", "api", "Chart.yaml"),
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}, "unexpected release add command")
	requireStringContains(t, spec.Stages[1].FileUpdates[1].Content, `url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"`, "unexpected formula update")
	requireStringContains(t, spec.Stages[1].FileUpdates[2].Content, `"version": "1.4.2"`, "unexpected scoop version update")
	requireStringContains(t, spec.Stages[1].FileUpdates[2].Content, `"extract_dir": "erun-1.4.2"`, "unexpected scoop extract dir update")
	requireEqual(t, spec.Stages[2].Name, "push-release-tag", "unexpected tag push stage")
	requireDeepEqual(t, spec.Stages[2].GitCommands[0].Args, []string{"push", "origin", "v1.4.2"}, "unexpected tag push command")
	requireCondition(t, spec.Stages[3].Name == "sync-packaging-checksums" && spec.Stages[3].PackagingSync != nil, "unexpected packaging sync stage: %+v", spec.Stages[3])
	requireDeepEqual(t, spec.Stages[3].GitCommands[0].Args, []string{
		"add",
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}, "unexpected packaging add command")
	requireDeepEqual(t, spec.Stages[3].GitCommands[1].Args, []string{"commit", "-m", "[skip ci] sync package metadata 1.4.2"}, "unexpected packaging commit command")
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
	err = runReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
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
    "emcp.exe",
    "eapi.exe"
  ]
}
`,
			},
		}, nil
	})
	requireNoError(t, err, "RunReleaseSpec failed")

	formulaData, err := os.ReadFile(filepath.Join(projectRoot, "Formula", "erun.rb"))
	requireNoError(t, err, "read formula")
	requireBytesContains(t, formulaData, `url "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.tar.gz"`, "unexpected formula URL")
	requireBytesContains(t, formulaData, `sha256 "formula-checksum"`, "unexpected formula checksum")

	scoopData, err := os.ReadFile(filepath.Join(projectRoot, "bucket", "erun.json"))
	requireNoError(t, err, "read scoop manifest")
	scoop := string(scoopData)
	requireStablePackagingFiles(t, scoop)
	requireStablePackagingGitCalls(t, gitCalls, projectRoot)
}

func requireStablePackagingFiles(t *testing.T, scoop string) {
	t.Helper()
	requireStringContains(t, scoop, `"version": "1.4.2"`, "unexpected scoop version")
	requireStringContains(t, scoop, `"url": "https://github.com/sophium/erun/archive/refs/tags/v1.4.2.zip"`, "unexpected scoop URL")
	requireStringContains(t, scoop, `"extract_dir": "erun-1.4.2"`, "unexpected scoop extract dir")
	requireStringContains(t, scoop, `"hash": "scoop-checksum"`, "unexpected scoop checksum")
}

func requireStablePackagingGitCalls(t *testing.T, gitCalls [][]string, projectRoot string) {
	t.Helper()
	requireDeepEqual(t, gitCalls[5], []string{projectRoot, "push", "origin", "v1.4.2"}, "unexpected tag push call")
	requireDeepEqual(t, gitCalls[6], []string{
		projectRoot,
		"add",
		filepath.Join("Formula", "erun.rb"),
		filepath.Join("bucket", "erun.json"),
	}, "unexpected packaging add call")
	requireDeepEqual(t, gitCalls[7], []string{projectRoot, "commit", "-m", "[skip ci] sync package metadata 1.4.2"}, "unexpected packaging commit call")
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
    "emcp.exe",
    "eapi.exe"
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
	writeReleaseProjectScaffold(t, releaseRoot)
	requireNoError(t, SaveProjectConfig(projectRoot, options.ProjectConfig), "SaveProjectConfig failed")
	if options.WithPackaging {
		writeReleasePackagingScaffold(t, projectRoot)
	}

	return projectRoot
}

func writeReleaseProjectScaffold(t *testing.T, releaseRoot string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(releaseRoot, 0o755), "mkdir release root")
	requireNoError(t, os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644), "write VERSION")
	writeReleaseChartScaffold(t, releaseRoot)
	writeReleaseDockerScaffold(t, releaseRoot)
}

func writeReleaseChartScaffold(t *testing.T, releaseRoot string) {
	t.Helper()
	chartPath := filepath.Join(releaseRoot, "k8s", "api")
	requireNoError(t, os.MkdirAll(chartPath, 0o755), "mkdir chart path")
	requireNoError(t, os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n"), 0o644), "write Chart.yaml")
}

func writeReleaseDockerScaffold(t *testing.T, releaseRoot string) {
	t.Helper()
	dockerPath := filepath.Join(releaseRoot, "docker", "api")
	requireNoError(t, os.MkdirAll(dockerPath, 0o755), "mkdir docker path")
	requireNoError(t, os.WriteFile(filepath.Join(dockerPath, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644), "write Dockerfile")
	otherDockerPath := filepath.Join(releaseRoot, "docker", "base")
	requireNoError(t, os.MkdirAll(otherDockerPath, 0o755), "mkdir other docker path")
	requireNoError(t, os.WriteFile(filepath.Join(otherDockerPath, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644), "write other Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(otherDockerPath, "VERSION"), []byte("9.9.9\n"), 0o644), "write other VERSION")
}

func writeReleasePackagingScaffold(t *testing.T, projectRoot string) {
	t.Helper()
	formulaPath := filepath.Join(projectRoot, "Formula")
	requireNoError(t, os.MkdirAll(formulaPath, 0o755), "mkdir Formula")
	requireNoError(t, os.WriteFile(filepath.Join(formulaPath, "erun.rb"), []byte(`class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.4.1.tar.gz"
  sha256 "unchanged"
  license "MIT"
end
`), 0o644), "write formula")

	bucketPath := filepath.Join(projectRoot, "bucket")
	requireNoError(t, os.MkdirAll(bucketPath, 0o755), "mkdir bucket")
	requireNoError(t, os.WriteFile(filepath.Join(bucketPath, "erun.json"), []byte(`{
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
    "emcp.exe",
    "eapi.exe"
  ]
}
`), 0o644), "write scoop manifest")
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
