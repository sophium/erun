package integration

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestSSHD(t *testing.T) {
	t.Parallel()
	t.Run("help", testSSHDHelp)

	t.Run("init_help", testSSHDInitHelp)

	t.Run("sync_help", testSSHDSyncHelp)

	t.Run("sync_refuses_an_env_with_no_pod_worktree", testSSHDSyncRefusesAnEnvWithNoPodWorktree)

	t.Run("sync_refuses_when_workspace_sync_is_off", testSSHDSyncRefusesWhenWorkspaceSyncIsOff)

	t.Run("sync_dry_run_traces_the_resolved_pass", testSSHDSyncDryRunTracesTheResolvedPass)

	t.Run("sync_real_run_drops_what_the_pod_no_longer_has", testSSHDSyncRealRunDropsWhatThePodNoLongerHas)

	t.Run("sync_real_run_signs_a_mirrored_darwin_artifact", testSSHDSyncRealRunSignsAMirroredDarwinArtifact)

	t.Run("sync_real_run_is_a_no_op_once_the_mirror_matches", testSSHDSyncRealRunIsANoOpOnceTheMirrorMatches)

	t.Run("sync_real_run_publishes_complete_files_and_refetches_nothing", testSSHDSyncRealRunPublishesCompleteFilesAndRefetchesNothing)

	t.Run("init_real_run_writes_local_ssh_config", testSSHDInitRealRunWritesLocalSSHConfig)

	t.Run("init_dry_run_traces_full_flow", testSSHDInitDryRunTracesFullFlow)

	t.Run("init_dry_run_workspace_sync_resolves_project_root", testSSHDInitDryRunWorkspaceSyncResolvesProjectRoot)

	t.Run("init_real_run_remote_exec_failure_surfaces_stderr", testSSHDInitRealRunRemoteExecFailureSurfacesStderr)

	t.Run("init_real_run_retries_after_pod_not_found", testSSHDInitRealRunRetriesAfterPodNotFound)
}

func testSSHDHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"sshd", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/help", normalize.Apply(result.Combined))
}

func testSSHDInitHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"sshd", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/init_help", normalize.Apply(result.Combined))
}

func testSSHDSyncHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"sshd", "sync", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/sync_help", normalize.Apply(result.Combined))
}

func testSSHDSyncRefusesAnEnvWithNoPodWorktree(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "remote-agent") {
		t.Fatalf("the refusal must name the precondition: %s", result.Combined)
	}
}

func testSSHDSyncRefusesWhenWorkspaceSyncIsOff(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "not enabled") {
		t.Fatalf("the refusal must name the precondition: %s", result.Combined)
	}
}

func testSSHDSyncDryRunTracesTheResolvedPass(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 47000)
	// An empty ssh listing keeps the counts deterministic.
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "ssh", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "ssh")...)
	result := erun.Run(t, []string{"sshd", "sync", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "would copy") {
		t.Fatalf("a dry run must report the counts a pass would change: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "resolve workspace sync") {
		t.Fatalf("a dry run must trace the pass it resolved: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "mirror/team-dev") {
		t.Fatalf("a dry run must name the host path it would mirror into: %s", result.Combined)
	}
}

// The mirror must drop what the pod no longer has.
func testSSHDSyncRealRunDropsWhatThePodNoLongerHas(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 47100)
	mirror := filepath.Join(setup.Home, "mirror", "team-dev")
	stale := filepath.Join(mirror, "gone-from-the-pod.txt")
	nested := filepath.Join(mirror, "pkg", "also-gone.go")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir mirror: %v", err)
	}
	for _, path := range []string{stale, nested} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed mirror %s: %v", path, err)
		}
	}

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "ssh", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "ssh")...)

	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, path := range []string{stale, nested} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be deleted from the mirror, stat err = %v", path, err)
		}
	}
	if !strings.Contains(result.Combined, "deleted 2") {
		t.Fatalf("the pass must report what it removed:\n%s", result.Combined)
	}
	if _, err := os.Stat(filepath.Join(mirror, "pkg")); !os.IsNotExist(err) {
		t.Fatalf("expected the emptied directory to be pruned, stat err = %v", err)
	}
}

// The pod has no codesign, so the mirror pass must sign darwin artifacts.
// ssh answers only the outputs listing; tar is stubbed because the artifact is seeded.
func testSSHDSyncRealRunSignsAMirroredDarwinArtifact(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 47300)
	artifacts := filepath.Join(setup.Home, "mirror", "team-dev", ".erun-outputs")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	artifact := filepath.Join(artifacts, "erun-darwin-arm64")
	if err := os.WriteFile(artifact, machOPayload, 0o444); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "ssh", "case \"$*\" in\n"+
		"  *.erun/outputs*) printf 'erun-darwin-arm64\\0' ;;\n"+
		"esac\n"+
		"exit 0")
	fixture.StubBinary(t, stubs, "tar", "")
	codesignLog := fixture.StubCodesign(t, stubs, fixture.CodesignStubSpec{})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "ssh", "tar", "codesign")...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")

	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if calls := readCodesignCalls(t, codesignLog); !strings.Contains(calls, "-s - -f") {
		t.Fatalf("expected the mirrored artifact to be ad-hoc signed, got codesign calls:\n%s", calls)
	}
	// Read-only must not stop the operator running the artifact.
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat mirrored artifact: %v", err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Fatalf("expected the mirrored artifact read-only and executable, got mode %v", info.Mode().Perm())
	}
	if !strings.Contains(result.Combined, "signed=1") {
		t.Fatalf("the pass log must report what it signed:\n%s", result.Combined)
	}
}

func testSSHDSyncRealRunIsANoOpOnceTheMirrorMatches(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 47200)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "ssh", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "ssh")...)

	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "copied 0 files, deleted 0") {
		t.Fatalf("an already-matching mirror must change nothing:\n%s", result.Combined)
	}
}

// A reader must never see a partially fetched file, and the publish must keep the
// pod's mtime or every pass re-fetches the tree.
func testSSHDSyncRealRunPublishesCompleteFilesAndRefetchesNothing(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 47300)
	mirror := filepath.Join(setup.Home, "mirror", "team-dev")

	body := bytes.Repeat([]byte("erun mirror payload\n"), 4096)
	archive := filepath.Join(setup.Cwd, "pod-archive.tar")
	const podMTime = 1700000000
	fixture.WriteTarArchive(t, archive, []fixture.TarEntry{{Path: "app/main.go", Body: body}}, time.Unix(podMTime, 0))

	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubWorkspaceSyncSSH(t, stubs, fixture.WorkspaceSyncSSHStubSpec{
		IndexPaths:  []string{"app/main.go"},
		StatLines:   []string{fmt.Sprintf("%d %d app/main.go", len(body), podMTime)},
		ArchivePath: archive,
	})...)

	result := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "copied 1 files") {
		t.Fatalf("the pass must report the file it fetched:\n%s", result.Combined)
	}
	mirrored := filepath.Join(mirror, "app", "main.go")
	got, err := os.ReadFile(mirrored)
	if err != nil {
		t.Fatalf("read mirrored file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("mirrored file is %d bytes, want the complete %d", len(got), len(body))
	}
	if files := mirrorFiles(t, mirror); len(files) != 1 || files[0] != "app/main.go" {
		t.Fatalf("mirror holds %v, want exactly [app/main.go]", files)
	}

	second := erun.Run(t, []string{"sshd", "sync", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if second.ExitCode != 0 {
		t.Fatalf("exit %d: %s", second.ExitCode, second.Combined)
	}
	if !strings.Contains(second.Combined, "copied 0 files") {
		t.Fatalf("an unchanged file must not be fetched again:\n%s", second.Combined)
	}
}

func testSSHDInitRealRunWritesLocalSSHConfig(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	sshConfig := filepath.Join(setup.Home, ".ssh", "config")
	raw, err := os.ReadFile(sshConfig)
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"Host erun-team-dev",
		"HostName 127.0.0.1",
		"Port 64022",
		"User erun",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected ~/.ssh/config to contain %q, got:\n%s", want, body)
		}
	}
}

func testSSHDInitDryRunTracesFullFlow(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022", "--dry-run"}
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/init_dry_run_traces_full_flow", normalize.Apply(result.Combined))
}

func testSSHDInitDryRunWorkspaceSyncResolvesProjectRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	body := "name: dev\n" +
		"repopath: /home/erun/git/team\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: remote-agent\n" +
		"sshd:\n" +
		"  workspacesync:\n" +
		"    enabled: true\n"
	if err := os.WriteFile(envCfg, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite env config with workspace sync: %v", err)
	}
	fixture.SeedGitRepo(t, setup.Cwd)
	publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022", "--dry-run"}
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/init_dry_run_workspace_sync_resolves_project_root", normalize.Apply(result.Combined))
}

func testSSHDInitRealRunRemoteExecFailureSurfacesStderr(t *testing.T) {
	// The exec stderr is not a pod-churn token, so init must not retry.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *" exec "*)`,
		`    printf '%s\n' 'error: unable to upgrade connection: Forbidden (user cannot exec into pod)' >&2`,
		`    exit 1 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
	// Confirm erun-devops published so the exec failure is what surfaces.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when remote exec fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "sshd/init_real_run_remote_exec_failure_surfaces_stderr", normalize.Apply(result.Combined))
}

func testSSHDInitRealRunRetriesAfterPodNotFound(t *testing.T) {
	// One retryable failure only: each retry costs a real 5-second delay.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	marker := filepath.Join(stubs, "exec-attempted")
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *" exec "*)`,
		`    if [ -f '` + marker + `' ]; then`,
		`      exit 0`,
		`    fi`,
		`    touch '` + marker + `'`,
		`    printf '%s\n' 'error: pod not found' >&2`,
		`    exit 1 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "sshd/init_real_run_retries_after_pod_not_found", normalize.Apply(result.Combined))
}

func mirrorFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk mirror: %v", err)
	}
	sort.Strings(files)
	return files
}
