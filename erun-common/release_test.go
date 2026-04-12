package eruncommon

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveReleaseSpecStableRelease(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
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
	if len(spec.Stages) != 4 {
		t.Fatalf("expected 4 stages, got %+v", spec.Stages)
	}
	if got := spec.Stages[0].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")}) {
		t.Fatalf("unexpected release add command: %+v", got)
	}
	if got := spec.Stages[0].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"commit", "-m", "[skip ci] release 1.4.2"}) {
		t.Fatalf("unexpected release commit command: %+v", got)
	}
	if got := spec.Stages[0].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"tag", "-a", "v1.4.2", "-m", "Release 1.4.2"}) {
		t.Fatalf("unexpected release tag command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"add", filepath.Join("erun-devops", "VERSION")}) {
		t.Fatalf("unexpected bump add command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"checkout", "develop"}) {
		t.Fatalf("unexpected sync checkout command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"merge", "--no-edit", "-X", "theirs", "main"}) {
		t.Fatalf("unexpected sync merge command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"checkout", "main"}) {
		t.Fatalf("unexpected sync return command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "main", "develop"}) {
		t.Fatalf("unexpected push command: %+v", got)
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
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if spec.Mode != ReleaseModeCandidate || spec.Version != "1.4.2-rc.abc1234" || spec.NextVersion != "" {
		t.Fatalf("unexpected release spec: %+v", spec)
	}
	if len(spec.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %+v", spec.Stages)
	}
	if got := spec.Stages[0].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234"}) {
		t.Fatalf("unexpected candidate tag command: %+v", got)
	}
	if got := spec.Stages[1].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "integration"}) {
		t.Fatalf("unexpected candidate push command: %+v", got)
	}
}

func TestRunReleaseSpecWritesFilesAndRunsGitStages(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "main", nil },
		func(string) (string, error) { return "abc1234", nil },
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
	}); err != nil {
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

func TestRunReleaseSpecCandidateRunsTagAndPush(t *testing.T) {
	projectRoot := setupReleaseProject(t, releaseProjectOptions{})

	spec, err := resolveReleaseSpec(
		func() (string, string, error) { return "tenant-a", projectRoot, nil },
		LoadProjectConfig,
		func(string) (string, error) { return "develop", nil },
		func(string) (string, error) { return "abc1234", nil },
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
	}); err != nil {
		t.Fatalf("RunReleaseSpec failed: %v", err)
	}

	wantCalls := [][]string{
		{projectRoot, "add", filepath.Join("erun-devops", "k8s", "api", "Chart.yaml")},
		{projectRoot, "commit", "-m", "[skip ci] release 1.4.2-rc.abc1234"},
		{projectRoot, "tag", "-a", "v1.4.2-rc.abc1234", "-m", "Release candidate 1.4.2-rc.abc1234"},
		{projectRoot, "push", "--follow-tags", "origin", "develop"},
	}
	if !reflect.DeepEqual(gitCalls, wantCalls) {
		t.Fatalf("unexpected git calls: got %+v want %+v", gitCalls, wantCalls)
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
		ReleaseParams{},
	)
	if err != nil {
		t.Fatalf("resolveReleaseSpec failed: %v", err)
	}

	if got := spec.Stages[2].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"checkout", "integration"}) {
		t.Fatalf("unexpected configured sync checkout command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[1].Args; !reflect.DeepEqual(got, []string{"merge", "--no-edit", "-X", "theirs", "trunk"}) {
		t.Fatalf("unexpected configured sync merge command: %+v", got)
	}
	if got := spec.Stages[2].GitCommands[2].Args; !reflect.DeepEqual(got, []string{"checkout", "trunk"}) {
		t.Fatalf("unexpected configured sync return command: %+v", got)
	}
	if got := spec.Stages[3].GitCommands[0].Args; !reflect.DeepEqual(got, []string{"push", "--follow-tags", "origin", "trunk", "integration"}) {
		t.Fatalf("unexpected configured push command: %+v", got)
	}
}

type releaseProjectOptions struct {
	ProjectConfig ProjectConfig
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

	return projectRoot
}
