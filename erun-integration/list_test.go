package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestList(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/help", normalize.Apply(result.Combined))
	})

	t.Run("empty_config", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "list/empty_config", normalize.Apply(result.Combined))
	})

	t.Run("with_seeded_tenant_env", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_seeded_tenant_env", normalize.Apply(result.Combined))
	})

	t.Run("with_seeded_host_env", func(t *testing.T) {
		// A host env lists like any other environment, with its type named
		// plainly — it is not shown as a pod that failed to start.
		setup := env.New(t)
		fixture.SeedHostTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_seeded_host_env", normalize.Apply(result.Combined))
	})

	t.Run("platform_account_env_shows_enabled", func(t *testing.T) {
		// An env flagged platformaccount:true surfaces `platform-account: enabled`
		// in its detail block, so an operator can see the env holds cluster-admin.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"platformaccount: true\n")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/platform_account_env_shows_enabled", normalize.Apply(result.Combined))
	})

	t.Run("corrupted_env_config_errors", func(t *testing.T) {
		// A corrupted env config.yaml must fail list outright, not be silently skipped.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		mustWrite(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"), "{{{ not yaml")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a corrupted env config, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/corrupted_env_config_errors", normalize.Apply(result.Combined))
	})

	t.Run("corrupted_tenant_config_errors", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		mustWrite(t, filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml"), "{{{ not yaml")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a corrupted tenant config, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/corrupted_tenant_config_errors", normalize.Apply(result.Combined))
	})

	t.Run("tenant_api_url_and_envless_tenant", func(t *testing.T) {
		// A tenant with a config but no env subdirectories shows "environments: none", not an empty list.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		tenantCfg := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		mustWrite(t, tenantCfg,
			"projectroot: "+setup.Cwd+"\n"+
				"name: team\n"+
				"defaultenvironment: dev\n"+
				"api_url: https://api.example/erun\n")
		envlessDir := filepath.Join(setup.ConfigHome, "erun", "envless")
		if err := os.MkdirAll(envlessDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", envlessDir, err)
		}
		mustWrite(t, filepath.Join(envlessDir, "config.yaml"), "name: envless\nprojectroot: "+setup.Home+"\n")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/tenant_api_url_and_envless_tenant", normalize.Apply(result.Combined))
	})

	t.Run("explicit_runtime_type", func(t *testing.T) {
		// An explicit `type:` must win over the legacy fallback in the resolver.
		setup := env.New(t)
		seedExplicitTypeEnv(t, setup, "team", "prod", "runtime")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/explicit_runtime_type", normalize.Apply(result.Combined))
	})

	t.Run("legacy_remote_yaml_migrates_to_runtime", func(t *testing.T) {
		// A legacy remote=true env with no build-here signal migrates to Type=runtime on
		// read, preserving the old remote-worktree / no-local-build semantics the retired
		// remote/snapshot fields used to provide.
		setup := env.New(t)
		fixture.SeedLegacyRemoteTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/legacy_remote_yaml_migrates_to_runtime", normalize.Apply(result.Combined))
	})

	t.Run("multi_tenant_with_multiple_envs", func(t *testing.T) {
		// Distinct port ranges are assigned per tenant by tenant index.
		setup := env.New(t)
		seedListMultiTenant(t, setup)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/multi_tenant_with_multiple_envs", normalize.Apply(result.Combined))
	})

	t.Run("cwd_overrides_default_tenant", func(t *testing.T) {
		// cwd under a non-default tenant's project root marks that tenant `effective`, overriding the configured default.
		setup := env.New(t)
		seedListMultiTenant(t, setup)
		// cwd resolution walks up to a git root, so tenant-b's project root must be a real git repo.
		tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
		fixture.SeedGitRepo(t, tenantBPath)
		nested := filepath.Join(tenantBPath, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: nested, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/cwd_overrides_default_tenant", normalize.Apply(result.Combined))
	})

	t.Run("sshd_configured", func(t *testing.T) {
		setup := env.New(t)
		seedListSSHDTenant(t, setup)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/sshd_configured", normalize.Apply(result.Combined))
	})

	t.Run("with_cloud_providers_and_runtime_details", func(t *testing.T) {
		// The aws stub fails `sts get-caller-identity` deterministically; without it the
		// developer's real aws CLI would shape the status line and drift the golden between machines.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		root := filepath.Join(setup.ConfigHome, "erun")
		mustWrite(t, filepath.Join(root, "config.yaml"),
			"defaulttenant: team\n"+
				"cloudproviders:\n"+
				"  - alias: test-user+123456789012@aws\n"+
				"    provider: aws\n"+
				"    username: test-user\n"+
				"    accountid: \"123456789012\"\n"+
				"    profile: test-profile\n",
		)
		mustWrite(t, filepath.Join(root, "team", "dev", "config.yaml"),
			"name: dev\n"+
				"repopath: "+setup.Cwd+"\n"+
				"kubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\n"+
				"runtimeversion: 1.0.0\n"+
				"cloudprovideralias: test-user+123456789012@aws\n"+
				"runtimepod:\n"+
				"  cpu: \"2\"\n"+
				"  memory: 4Gi\n"+
				"idle:\n"+
				"  timeout: 10m\n"+
				"  workinghours: 09:00-18:00\n"+
				"  timezone: Europe/Riga\n"+
				"  idletrafficbytes: 2048\n",
		)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "aws", fixture.StubBinarySpec{
			Stderr:   "The config profile (test-profile) could not be found",
			ExitCode: 255,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_cloud_providers_and_runtime_details", normalize.Apply(result.Combined))
	})

	t.Run("with_claude_config", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		mustWrite(t, envCfg,
			"name: dev\n"+
				"repopath: "+setup.Cwd+"\n"+
				"kubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\n"+
				"runtimeversion: 1.0.0\n"+
				"claude:\n"+
				"  usemantle: true\n"+
				"  usebedrock: true\n"+
				"  models: [opus, sonnet, haiku]\n"+
				"  maxoutputtokens: 16384\n",
		)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_claude_config", normalize.Apply(result.Combined))
	})

	// The sizing recommendation's four directions, driven from a retained
	// history rather than from a live container. The history is the recommender's
	// whole input, so seeding it is what makes each direction reachable — a real
	// 26-hour observation window is not something a scenario can wait for, and
	// the shrink gate exists precisely to require one.
	//
	// Each seeds an env whose declared runtimepod is deliberately absent, which
	// is the in-pod reality: the chart injects the container's limits and the
	// pod's own config never learns them. The lines must therefore score against
	// the cgroup limit in the history, not against the defaults an empty
	// runtimepod normalizes to.

	t.Run("runtime_sizing_lowers_memory_on_a_long_quiet_window", func(t *testing.T) {
		// erun/build's measured shape: a 23552Mi limit whose high-water sat at
		// roughly half, with no kills and not one throttled period.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 31, samples: 240,
			peakMemoryBytes: 12742377472, limitBytes: 24696061952,
			quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_lowers_memory_on_a_long_quiet_window", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_raises_memory_on_an_oom_kill", func(t *testing.T) {
		// One kill outranks a comfortable-looking peak and needs no window: this
		// history has watched a single minute.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 0, samples: 2,
			peakMemoryBytes: 1073741824, limitBytes: 2147483648, oomKills: 1,
			quotaMilli: 1000, periods: 5000, throttled: 0, peakCPUMilli: 300,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_raises_memory_on_an_oom_kill", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_raises_cpu_on_sustained_throttling", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 31, samples: 240,
			peakMemoryBytes: 1073741824, limitBytes: 8589934592,
			quotaMilli: 2000, periods: 100000, throttled: 12000, peakCPUMilli: 1900,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_raises_cpu_on_sustained_throttling", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_raises_memory_when_the_peak_nears_the_limit", func(t *testing.T) {
		// No kill yet, but the high-water is inside the raise margin. Sampling
		// means the true peak is at least this, so the raise is high confidence
		// without waiting for the environment to be killed first.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 5, samples: 30,
			peakMemoryBytes: 2040109465, limitBytes: 2147483648,
			quotaMilli: 1000, periods: 50000, throttled: 0, peakCPUMilli: 600,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_raises_memory_when_the_peak_nears_the_limit", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_bounds_a_raise_by_the_namespace_quota", func(t *testing.T) {
		// A namespace ResourceQuota counts every container in the pod, so a raise
		// past the quota less the dind sidecar's own limit is a size nothing
		// would schedule. The verdict says it clamped rather than quietly
		// recommending a quota change too.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"namespacequota:\n  cpu: \"8\"\n  memory: 32Gi\n  storage: 80Gi\n")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 5, samples: 30,
			peakMemoryBytes: 21474836480, limitBytes: 21474836480, oomKills: 3,
			quotaMilli: 4000, periods: 50000, throttled: 0, peakCPUMilli: 1000,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_bounds_a_raise_by_the_namespace_quota", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_holds_cpu_on_tolerable_throttling", func(t *testing.T) {
		// frs/local's measured 425 of 308631 periods. Sub-threshold throttling is
		// tolerable, not unused: it must neither grow the environment nor license
		// a shrink. This is the false-positive check.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 31, samples: 240,
			peakMemoryBytes: 1027301376, limitBytes: 2147483648,
			quotaMilli: 4000, periods: 308631, throttled: 425, peakCPUMilli: 300,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_holds_cpu_on_tolerable_throttling", normalize.Apply(result.Combined))
	})

	t.Run("runtime_sizing_withholds_a_shrink_on_a_short_window", func(t *testing.T) {
		// The same comfortable peak as the shrink scenario, watched for an hour.
		// The verdict must be `insufficient-evidence` naming the shortfall, not a
		// shrink and not a silent hold.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 1, samples: 120,
			peakMemoryBytes: 12742377472, limitBytes: 24696061952,
			quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
		})
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/runtime_sizing_withholds_a_shrink_on_a_short_window", normalize.Apply(result.Combined))
	})
}

type usageHistorySpec struct {
	windowHours     int
	samples         int
	peakMemoryBytes int64
	limitBytes      int64
	oomKills        int64
	quotaMilli      int64
	periods         int64
	throttled       int64
	peakCPUMilli    int64
}

// seedUsageHistory writes the retained usage store directly. The on-disk shape
// is the contract between the in-pod monitor that writes it and every reader, so
// spelling the JSON out here pins that shape rather than round-tripping through
// the same structs it is meant to check.
func seedUsageHistory(t testing.TB, setup env.Setup, tenant, environment string, spec usageHistorySpec) {
	t.Helper()
	dir := filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Fixed instants, so the observed window is a property of the fixture rather
	// than of when the suite happened to run.
	first := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	last := first.Add(time.Duration(spec.windowHours)*time.Hour + 12*time.Minute)
	sample := fmt.Sprintf(
		`{"cpu":{"quotaCores":%g,"usageUsec":%d,"periods":%d,"throttledPeriods":%d},"memory":{"limitBytes":%d,"currentBytes":%d,"peakBytes":%d,"oomKills":%d}}`,
		float64(spec.quotaMilli)/1000, spec.periods*100, spec.periods, spec.throttled,
		spec.limitBytes, spec.peakMemoryBytes/2, spec.peakMemoryBytes, spec.oomKills)
	samples := make([]string, 0, spec.samples)
	for i := 0; i < spec.samples; i++ {
		samples = append(samples, sample)
	}
	body := fmt.Sprintf(
		`{"firstObservedAt":%q,"lastObservedAt":%q,"observedPeakMemoryBytes":%d,"observedOomKills":%d,"observedPeakCpuMilli":%d,"observedPeriods":%d,"observedThrottledPeriods":%d,"samples":[%s]}`,
		first.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano), spec.peakMemoryBytes,
		spec.oomKills, spec.peakCPUMilli, spec.periods, spec.throttled, strings.Join(samples, ","))
	mustWrite(t, filepath.Join(dir, "usage-history.json"), body+"\n")
}

// seedExplicitTypeEnv is shared by list and delete scenarios that assert the resolver honors an explicit `type:`.
func seedExplicitTypeEnv(t testing.TB, setup env.Setup, tenant, environment, envType string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"type: "+envType+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n",
	)
}

func seedListMultiTenant(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: tenant-a\n")
	tenantAPath := filepath.Join(setup.Home, "git", "tenant-a")
	tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo %s: %v", dir, err)
		}
	}
	writeListTenantConfig(t, root, "tenant-a", "local", tenantAPath)
	writeListTenantConfig(t, root, "tenant-b", "dev", tenantBPath)
	writeListEnvConfig(t, root, "tenant-a", "local", tenantAPath, "cluster-local", "")
	writeListEnvConfig(t, root, "tenant-a", "prod", tenantAPath, "cluster-prod", "")
	writeListEnvConfig(t, root, "tenant-b", "dev", tenantBPath, "cluster-b", "")
}

func seedListSSHDTenant(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: tenant-a\n")
	tenantPath := filepath.Join(setup.Home, "git", "tenant-a")
	if err := os.MkdirAll(tenantPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	writeListTenantConfig(t, root, "tenant-a", "dev", tenantPath)
	envDir := filepath.Join(root, "tenant-a", "dev")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWrite(t, filepath.Join(root, "tenant-a", "dev", "config.yaml"),
		"name: dev\n"+
			"repopath: "+tenantPath+"\n"+
			"kubernetescontext: cluster-dev\n"+
			"type: remote-agent\n"+
			"sshd:\n"+
			"  enabled: true\n"+
			"  localport: 17022\n"+
			"  publickeypath: /tmp/id_ed25519.pub\n",
	)
}

func writeListTenantConfig(t testing.TB, root, tenant, defaultEnv, projectRoot string) {
	t.Helper()
	tenantDir := filepath.Join(root, tenant)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", tenantDir, err)
	}
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"name: "+tenant+"\n"+
			"projectroot: "+projectRoot+"\n"+
			"defaultenvironment: "+defaultEnv+"\n",
	)
}

func writeListEnvConfig(t testing.TB, root, tenant, environment, repoPath, kubeContext, registry string) {
	t.Helper()
	envDir := filepath.Join(root, tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	body := "name: " + environment + "\n" +
		"repopath: " + repoPath + "\n" +
		"kubernetescontext: " + kubeContext + "\n"
	if registry != "" {
		body += "containerregistry: " + registry + "\n"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), body)
}

func mustWrite(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
