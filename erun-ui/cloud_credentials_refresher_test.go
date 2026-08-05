package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// credentialRefreshTargetApp builds an app whose store holds one env of the
// given type with the given alias attached, plus the AWS and Cloudflare
// providers those aliases can resolve to.
func credentialRefreshTargetApp(t *testing.T, envType eruncommon.EnvironmentType, alias string) *App {
	t.Helper()
	projectRoot := t.TempDir()
	return NewApp(erunUIDeps{
		store: stubUIStore{
			config: &eruncommon.ERunConfig{
				CloudProviders: []eruncommon.CloudProviderConfig{
					{Alias: "op+123@aws", Provider: eruncommon.CloudProviderAWS, Username: "op", AccountID: "123", Profile: "op"},
					{Alias: "op@cloudflare", Provider: eruncommon.CloudProviderCloudflare, Username: "op"},
				},
			},
			tenants: map[string]eruncommon.TenantConfig{
				"team": {Name: "team", DefaultEnvironment: "dev"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"team/dev": {
					Name:               "dev",
					Type:               envType,
					LocalRepoPath:      projectRoot,
					KubernetesContext:  "ctx",
					CloudProviderAlias: alias,
				},
			},
		},
		findProjectRoot: func() (string, string, error) { return "team", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
	})
}

// TestResolveCloudCredentialsRefreshTargetCoversEveryEnvTypeWithAnAWSAlias locks
// the fix: the chart wires AWS_PROFILE=erun-host into the runtime container for
// any env with an AWS alias attached, so the refresher that writes that profile
// must run for any such env too. Gating it on worktree location left local-agent
// pods pointing at a profile nothing ever created.
func TestResolveCloudCredentialsRefreshTargetCoversEveryEnvTypeWithAnAWSAlias(t *testing.T) {
	for _, envType := range []eruncommon.EnvironmentType{
		eruncommon.EnvironmentTypeLocalAgent,
		eruncommon.EnvironmentTypeRemoteAgent,
		eruncommon.EnvironmentTypeRuntime,
	} {
		t.Run(string(envType), func(t *testing.T) {
			app := credentialRefreshTargetApp(t, envType, "op+123@aws")
			alias, _, ok := app.resolveCloudCredentialsRefreshTarget(uiSelection{Tenant: "team", Environment: "dev"})
			if !ok {
				t.Fatalf("expected an AWS alias on a %s env to start the refresher", envType)
			}
			if alias != "op+123@aws" {
				t.Fatalf("alias = %q, want op+123@aws", alias)
			}
		})
	}
}

// TestResolveCloudCredentialsRefreshTargetSkipsWithoutAnAWSAlias keeps the
// remaining gates honest now that env type no longer participates: no alias, or
// a non-AWS one, must still leave the pod alone.
func TestResolveCloudCredentialsRefreshTargetSkipsWithoutAnAWSAlias(t *testing.T) {
	for name, alias := range map[string]string{"no_alias": "", "cloudflare_alias": "op@cloudflare"} {
		t.Run(name, func(t *testing.T) {
			app := credentialRefreshTargetApp(t, eruncommon.EnvironmentTypeLocalAgent, alias)
			if _, _, ok := app.resolveCloudCredentialsRefreshTarget(uiSelection{Tenant: "team", Environment: "dev"}); ok {
				t.Fatalf("expected alias %q to leave the refresher stopped", alias)
			}
		})
	}
}

func TestNextCredentialRefreshDelayClampsToInterval(t *testing.T) {
	now := time.Now()
	delay := nextCredentialRefreshDelay(now.Add(2 * time.Hour))
	if delay != credentialRefreshInterval {
		t.Fatalf("expected delay clamped to %v, got %v", credentialRefreshInterval, delay)
	}
}

func TestNextCredentialRefreshDelayShortensForSoonExpiration(t *testing.T) {
	now := time.Now()
	expiration := now.Add(20 * time.Minute)
	delay := nextCredentialRefreshDelay(expiration)
	want := time.Until(expiration) - credentialRefreshLeadTime
	if delay > want+time.Second || delay < want-time.Second {
		t.Fatalf("expected delay ~%v for expiration in 20m, got %v", want, delay)
	}
}

func TestNextCredentialRefreshDelayUsesBackoffOnExpiredCreds(t *testing.T) {
	if got := nextCredentialRefreshDelay(time.Now().Add(-time.Hour)); got != credentialRefreshBackoff {
		t.Fatalf("expected expired credentials to schedule short backoff, got %v", got)
	}
}

func TestNextCredentialRefreshDelayUsesIntervalWhenExpirationUnknown(t *testing.T) {
	if got := nextCredentialRefreshDelay(time.Time{}); got != credentialRefreshInterval {
		t.Fatalf("expected default interval when expiration is zero, got %v", got)
	}
}
