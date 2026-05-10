package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDeploy(t *testing.T) {
	t.Run("help_outside_devops_cwd", func(t *testing.T) {
		// Regression for commit a7b4d08: when cwd has no devops context, the
		// deploy command must still be registered so the desktop UI's
		// `erun deploy <tenant> <env> --version X` invocation can land its
		// flags. Pre-fix, this returned the root help and "unknown flag:
		// --version". Lives unskipped so the integration suite fails until
		// erun-cli/cmd/root.go always registers deployCommand().
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/help_outside_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("version_flag_recognized_outside_devops_cwd", func(t *testing.T) {
		// A second regression check: even when the flag is set on a real
		// deploy attempt, "unknown flag: --version" must not appear. The
		// command will still fail (no env or no chart) but for a sensible
		// reason rather than flag parsing. Lives unskipped so the suite
		// fails until the deploy registration fix lands.
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "missing", "missing", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/version_flag_recognized_outside_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_cwd", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("force_flag_appears_in_trace", func(t *testing.T) {
		// --force surfaces in the deploy trace so dry-run callers can see
		// the cache-bypass decision, and it propagates to the resolved
		// plan: SkipHelm cannot short-circuit when fingerprint promotion
		// is disabled, so the helm upgrade always runs.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--force", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/force_flag_appears_in_trace", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_outside_devops_with_tenant_env", func(t *testing.T) {
		// Regression for issue #252: when erun deploy <tenant> <env> is
		// invoked from a cwd outside the devops module (e.g. the desktop
		// UI launching the binary from $HOME for a remote environment),
		// the resolved tenant project root must drive chart discovery
		// instead of cwd. Pre-fix this hit "helm chart not found in
		// current directory" because resolveCurrentDevopsK8sDir gated
		// chart resolution on cwd == projectRoot.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_outside_devops_with_tenant_env", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_uses_embedded_chart", func(t *testing.T) {
		// Regression: a remote env (Remote=true) has its repopath on the
		// remote host's filesystem (e.g. proxmox1: /home/erun/git/erun) and
		// has no local checkout at all. Deploy from any cwd must still
		// work: the embedded default-devops chart is materialized to a
		// temp dir and used for the helm install. Pre-fix, deploy stat'd
		// the remote repopath locally and failed with
		// "open <remote-path>: no such file or directory".
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		// Note: no SeedDevopsRepo — there is no local checkout anywhere.
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_remote_env_uses_embedded_chart", normalize.Apply(result.Combined))
	})

	t.Run("default_skips_optin_backend_charts", func(t *testing.T) {
		// Regression for issue #271: when a tenant repo contains the runtime
		// chart and the three opt-in backend charts, `erun deploy` without
		// --components must deploy only the runtime chart. The backend
		// charts ship as separate Helm releases and are gated behind the
		// --components flag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/default_skips_optin_backend_charts", normalize.Apply(result.Combined))
	})

	t.Run("components_includes_backend_in_deploy_order", func(t *testing.T) {
		// With --components, the opt-in backend charts must deploy in the
		// fixed dependency order (postgres -> db -> api -> runtime),
		// regardless of the order they appear on the command line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "erun-backend-api,erun-backend-db,erun-backend-postgres",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/components_includes_backend_in_deploy_order", normalize.Apply(result.Combined))
	})

	t.Run("project_k8s_plan_groups_parallel_step", func(t *testing.T) {
		// When .erun/config.yaml declares a k8s.deployments plan with a
		// parallel-group step (a list as the item), deploy must group those
		// charts into one step and emit a single "step N (parallel): ..."
		// trace line. Other steps stay serial. Order across steps matches
		// the config, not the alphabetical chart-discovery order.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "erun-backend-postgres,erun-backend-db,erun-backend-api",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/project_k8s_plan_groups_parallel_step", normalize.Apply(result.Combined))
	})

	t.Run("project_k8s_plan_includes_listed_charts_without_components_flag", func(t *testing.T) {
		// Listing a chart under environments.<env>.k8s.deployments must
		// imply --components for it: a user who has configured the plan
		// should not also have to pass --components=erun-backend-... on
		// every deploy. Without this, the opt-in filter would silently
		// strip the backend charts even though the plan named them.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/project_k8s_plan_includes_listed_charts_without_components_flag", normalize.Apply(result.Combined))
	})

	t.Run("project_k8s_plan_rejects_invalid_step_node", func(t *testing.T) {
		// A k8s.deployments step must be either a component name or a
		// list of component names. Anything else (a mapping, a number, …)
		// must surface as a clear error from the project config loader,
		// not silently parse to an empty step.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - {name: erun-devops}\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for malformed k8s.deployments step, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/project_k8s_plan_rejects_invalid_step_node", normalize.Apply(result.Combined))
	})

	t.Run("components_rejects_unknown_name", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "bogus",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown component, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/components_rejects_unknown_name", normalize.Apply(result.Combined))
	})

	t.Run("snapshot_conflict_errors", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--snapshot", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting snapshot flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/snapshot_conflict_errors", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Drive the non-dry-run helm/kubectl runners via stub binaries so
		// the deploy execution path (deploy.go's run* helpers, post-helm
		// kubectl waits, helm-recovery branches) gets covered.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "deploy/real_run_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_managed_cloud_traces_helm_set_strings", func(t *testing.T) {
		// Exercises eruncommon.applyCloudProviderDeployMetadata,
		// findCloudContextForKubernetesContext, cloudContextRegionFromName,
		// and the managed-cloud helm --set-string lines that come from
		// per-tenant cloud provider/context resolution. Seeds an env with
		// managedcloud=true, cloudprovideralias=dev, and a matching cloud
		// context that points at the same kubernetes context.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		root := filepath.Join(setup.ConfigHome, "erun")
		tenantDir := filepath.Join(root, "managed")
		envDir := filepath.Join(tenantDir, "prod")
		for _, dir := range []string{tenantDir, envDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
			[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
			t.Fatalf("tenant cfg: %v", err)
		}
		envBody := "name: prod\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\n"
		if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
			t.Fatalf("env cfg: %v", err)
		}
		fixture.SeedDevopsRepo(t, setup, "managed", "prod")
		result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_with_managed_cloud_traces_helm_set_strings", normalize.Apply(result.Combined))
	})

	t.Run("real_run_helm_pending_recovery_via_auto_recover_env", func(t *testing.T) {
		// Exercises wrapHelmDeployWithReleaseRecovery + the production
		// helm-recovery path: a stubbed `helm` exits with the pending
		// upgrade error message on the first invocation, succeeds on
		// the retry, and exits 0 for the recovery rollback. The new
		// ERUN_AUTO_RECOVER_HELM=1 env var bypasses the interactive
		// confirmation prompt so this can run from the harness.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "docker", "")
		// Counter-driven helm stub: print the pending-operation message
		// on the first `upgrade --install` call, exit 0 on every other
		// invocation (rollback recovery + retry upgrade).
		counter := filepath.Join(stubs, "helm-counter")
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`first_arg="$1"`,
			`if [ "$first_arg" = "upgrade" ]; then`,
			`  count=0`,
			`  if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
			`  count=$((count + 1))`,
			`  printf '%s' "$count" > '` + counter + `'`,
			`  if [ "$count" = "1" ]; then`,
			`    printf '%s\n' "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2`,
			`    exit 1`,
			`  fi`,
			`fi`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_AUTO_RECOVER_HELM=1")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_helm_pending_recovery_via_auto_recover_env", normalize.Apply(result.Combined))
	})

	t.Run("real_run_pod_watch_logs_clean_rollout", func(t *testing.T) {
		// Exercises the in-flight pod watcher started by DeployHelmChart.
		// The kubectl stub returns a pod owned by this helm release with
		// every container Running+Ready, so the watcher prints a single
		// status line and lets helm finish naturally. Locks the dry-run
		// trace's "watching pods" promise to a real-run summary line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: cleanRolloutPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "    pod team-devops-aaaaaa: erun-devops Running (Ready), erun-dind Running (Ready)") {
			t.Fatalf("missing pod-watch summary line in output:\n%s", out)
		}
		if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
			t.Fatalf("expected clean deploy completion in output:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_aborts_on_image_pull_backoff", func(t *testing.T) {
		// kubectl stub reports a pod with one container in
		// ImagePullBackOff. helm sleeps so the watcher fires first and
		// kills it. Locks the structured early-fail error message.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: imagePullBackOffPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "    pod team-devops-7d4b4c: erun-dind Waiting (ImagePullBackOff)") {
			t.Fatalf("missing pod-watch summary line in output:\n%s", out)
		}
		if !strings.Contains(out, `deploy failed early: pod team-devops-7d4b4c container erun-dind ImagePullBackOff: Back-off pulling image "ghcr.io/sophium/erun-dind:<VERSION>"`) {
			t.Fatalf("missing structured early-fail error in output:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_aborts_on_crashloop_after_threshold", func(t *testing.T) {
		// kubectl stub reports a CrashLoopBackOff with restartCount above
		// the threshold. The watcher kills helm and surfaces the last
		// terminated message so the user sees why the container is
		// crashing, not just helm's generic timeout.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: crashLoopPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy failed early: pod team-devops-crash container erun-devops CrashLoopBackOff") {
			t.Fatalf("missing structured early-fail error in output:\n%s", out)
		}
		if !strings.Contains(out, "exited with code 137") {
			t.Fatalf("missing last-terminated message in output:\n%s", out)
		}
	})

	t.Run("dry_run_dedup_skip_when_identical_marker_alive", func(t *testing.T) {
		// When another erun deploy is in flight against the same release with
		// the same params hash, dry-run reports "would skip" and exits 0.
		// We seed the marker with our own pid (always alive during this
		// test) and the params hash erun --dry-run will compute on the
		// first run. The second run sees the live identical marker.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// First dry-run: capture the params hash from "dedup: ready (..., hash=<HASH>)".
		first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if first.ExitCode != 0 {
			t.Fatalf("first dry-run exited %d:\n%s", first.ExitCode, first.Combined)
		}
		hash := extractDedupHash(t, first.Combined)
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			ParamsHash:  hash,
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		second := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if second.ExitCode != 0 {
			t.Fatalf("second dry-run exited %d:\n%s", second.ExitCode, second.Combined)
		}
		out := normalize.Apply(second.Combined)
		if !strings.Contains(out, "dedup: would skip") {
			t.Fatalf("expected 'dedup: would skip' trace, got:\n%s", out)
		}
		if !strings.Contains(out, "==> Skipping team/dev <VERSION> (identical deploy already in progress)") {
			t.Fatalf("expected ==> Skipping info line, got:\n%s", out)
		}
	})

	t.Run("dry_run_dedup_conflict_on_different_params", func(t *testing.T) {
		// A live in-flight deploy with a different params hash should fail
		// the second invocation with HelmReleaseConcurrentDeployError so two
		// callers with conflicting intent surface the conflict instead of
		// stomping on each other's helm release.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0-other",
		})
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on conflicting deploy, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "another erun deploy is in progress") {
			t.Fatalf("expected concurrent-deploy error, got:\n%s", out)
		}
		if !strings.Contains(out, "release \"team-devops\"") {
			t.Fatalf("expected release pointer in error, got:\n%s", out)
		}
	})

	t.Run("dry_run_dedup_reclaim_when_marker_pid_dead", func(t *testing.T) {
		// A leftover marker whose pid is no longer running should not block
		// a fresh dry-run; it reports "would reclaim" so the user can see
		// the stale-state recovery path was taken.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// Use a real reaped child PID (positive, dead) instead of PID 0.
		// PID 0 short-circuits in isProcessAlive without ever calling
		// Signal(0); a reaped PID forces the live signal-error path that
		// surfaces darwin's os.ErrProcessDone vs. linux's ESRCH. Without
		// that distinction the marker stays "alive" on darwin and deploy
		// is locked out for the full 15-minute max-age fallback.
		deadPID := reapedChildPID(t)
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         deadPID,
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("dry-run exited %d (expected 0 since prior pid is dead):\n%s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "dedup: would reclaim") {
			t.Fatalf("expected 'dedup: would reclaim' trace, got:\n%s", out)
		}
	})
}

// extractDedupHash pulls the live params hash off a "dedup: ready (release=..., hash=<HEX>)"
// line emitted by erun deploy --dry-run -vv. Tests use this to seed an
// identical-hash marker for the dedup-skip path. The raw output is captured
// before normalization so the hash is the real 16-char hex value, not the
// <HASH> placeholder.
func extractDedupHash(t *testing.T, raw string) string {
	t.Helper()
	const marker = "dedup: ready ("
	idx := strings.Index(raw, marker)
	if idx < 0 {
		t.Fatalf("dedup-ready trace not found in:\n%s", raw)
	}
	rest := raw[idx:]
	hashIdx := strings.Index(rest, "hash=")
	if hashIdx < 0 {
		t.Fatalf("hash= field not found in dedup trace:\n%s", rest)
	}
	hashStart := hashIdx + len("hash=")
	end := hashStart
	for end < len(rest) && (isHex(rest[end])) {
		end++
	}
	if end == hashStart {
		t.Fatalf("could not parse hash from dedup trace:\n%s", rest)
	}
	return rest[hashStart:end]
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// reapedChildPID spawns a short-lived child, waits for it to exit, and
// returns its now-dead PID. Calling isProcessAlive against this PID
// exercises the real signal(0) error path: ESRCH on linux,
// os.ErrProcessDone on darwin. PID reuse is theoretically possible but
// vanishingly unlikely between the wait return and the marker read.
func reapedChildPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("seed reaped child: %v", err)
	}
	pid := cmd.ProcessState.Pid()
	if pid <= 0 {
		t.Fatalf("seed reaped child returned invalid pid %d", pid)
	}
	return pid
}

const cleanRolloutPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-aaaaaa",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {"name": "erun-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}},
          {"name": "erun-dind", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}}
        ]
      }
    }
  ]
}`

const imagePullBackOffPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-7d4b4c",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "containerStatuses": [
          {
            "name": "erun-dind",
            "ready": false,
            "restartCount": 0,
            "state": {"waiting": {"reason": "ImagePullBackOff", "message": "Back-off pulling image \"ghcr.io/sophium/erun-dind:1.0.0\""}}
          }
        ]
      }
    }
  ]
}`

const crashLoopPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-crash",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {
            "name": "erun-devops",
            "ready": false,
            "restartCount": 3,
            "state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 5m restarting failed container"}},
            "lastState": {"terminated": {"reason": "Error", "exitCode": 137, "message": "exited with code 137"}}
          }
        ]
      }
    }
  ]
}`
