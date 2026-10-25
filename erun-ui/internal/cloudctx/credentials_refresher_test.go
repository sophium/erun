package cloudctx

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func credentialRefreshTargetService(t *testing.T, envType config.EnvironmentType, alias string) *Service {
	t.Helper()
	projectRoot := t.TempDir()
	return newTestService(t, fakeStore{
		config: config.ERunConfig{
			CloudProviders: []model.CloudProviderConfig{
				{Alias: "op+123@aws", Provider: config.CloudProviderAWS, Username: "op", AccountID: "123", Profile: "op"},
				{Alias: "op@cloudflare", Provider: config.CloudProviderCloudflare, Username: "op"},
			},
		},
		tenants: map[string]model.TenantConfig{
			"team": {Name: "team", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"team/dev": {
				Name:               "dev",
				Type:               envType,
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "ctx",
				CloudProviderAlias: alias,
			},
		},
	}, nil)
}

// The chart sets AWS_PROFILE=erun-host for any env with an AWS alias, so the refresher must run for every type.
func TestResolveCloudCredentialsRefreshTargetCoversEveryEnvTypeWithAnAWSAlias(t *testing.T) {
	for _, envType := range []config.EnvironmentType{
		config.EnvironmentTypeLocalAgent,
		config.EnvironmentTypeRemoteAgent,
		config.EnvironmentTypeRuntime,
	} {
		t.Run(string(envType), func(t *testing.T) {
			s := credentialRefreshTargetService(t, envType, "op+123@aws")
			alias, _, ok := s.resolveRefreshTarget(uimodel.Selection{Tenant: "team", Environment: "dev"})
			if !ok {
				t.Fatalf("expected an AWS alias on a %s env to start the refresher", envType)
			}
			if alias != "op+123@aws" {
				t.Fatalf("alias = %q, want op+123@aws", alias)
			}
		})
	}
}

func TestResolveCloudCredentialsRefreshTargetSkipsWithoutAnAWSAlias(t *testing.T) {
	for name, alias := range map[string]string{"no_alias": "", "cloudflare_alias": "op@cloudflare"} {
		t.Run(name, func(t *testing.T) {
			s := credentialRefreshTargetService(t, config.EnvironmentTypeLocalAgent, alias)
			if _, _, ok := s.resolveRefreshTarget(uimodel.Selection{Tenant: "team", Environment: "dev"}); ok {
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

func TestSurfaceRefreshFailureNotifiesTheSelection(t *testing.T) {
	notify := &notifications{}
	s := newTestService(t, fakeStore{}, notify)
	selection := uimodel.Selection{Tenant: "frs", Environment: "dev"}

	s.surfaceRefreshFailure(selection, errors.New("token expired"), nil)

	got := notify.all()
	if len(got) != 1 {
		t.Fatalf("expected one notification, got %+v", got)
	}
	if got[0].selection.Tenant != "frs" || got[0].selection.Environment != "dev" {
		t.Fatalf("unexpected identity: %+v", got[0].selection)
	}
	if !strings.Contains(got[0].message, "token expired") {
		t.Fatalf("message must carry the cause, got %q", got[0].message)
	}
}

func TestSurfaceRefreshFailureNotifiesOnlyOnce(t *testing.T) {
	notify := &notifications{}
	s := newTestService(t, fakeStore{}, notify)
	selection := uimodel.Selection{Tenant: "frs", Environment: "dev"}
	notified := false

	s.surfaceRefreshFailure(selection, errors.New("token expired"), &notified)
	s.surfaceRefreshFailure(selection, errors.New("token expired"), &notified)

	if got := notify.all(); len(got) != 1 {
		t.Fatalf("expected exactly one notification across two calls, got %+v", got)
	}
}
