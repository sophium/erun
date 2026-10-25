package providerconfig

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestResolvedCloudAliasesFoldsLegacyScalarAndMap(t *testing.T) {
	env := config.EnvConfig{
		CloudProviderAlias: "alice+123456789012@aws",
		CloudProviderAliases: map[string]string{
			config.CloudProviderCloudflare: "ci+cf-acct-1@cloudflare",
		},
	}
	resolved := env.ResolvedCloudAliases()
	if resolved[config.CloudProviderAWS] != "alice+123456789012@aws" {
		t.Fatalf("aws slot = %q", resolved[config.CloudProviderAWS])
	}
	if resolved[config.CloudProviderCloudflare] != "ci+cf-acct-1@cloudflare" {
		t.Fatalf("cloudflare slot = %q", resolved[config.CloudProviderCloudflare])
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved aliases, got %d", len(resolved))
	}
}

func TestResolveTenantCloudProviderIssuersSkipsCloudflare(t *testing.T) {
	store := stubCloudContextStore{config: config.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		{Alias: "alice+123456789012@aws", Provider: config.CloudProviderAWS, OIDCIssuerURL: "https://issuer.example.com"},
		{Alias: "ci+cf-acct-1@cloudflare", Provider: config.CloudProviderCloudflare, Cloudflare: &model.CloudflareProviderConfig{AccountID: "cf-acct-1", TokenRef: "cloudflare/ci+cf-acct-1@cloudflare"}},
	}}}
	tenant := model.TenantConfig{CloudProviderAliases: []string{"alice+123456789012@aws", "ci+cf-acct-1@cloudflare"}}

	issuers, err := ResolveTenantCloudProviderIssuers(store, tenant)
	if err != nil {
		t.Fatalf("resolve issuers: %v", err)
	}
	if len(issuers) != 1 || issuers[0] != "https://issuer.example.com" {
		t.Fatalf("issuers = %v, want only the AWS issuer (Cloudflare has no OIDC)", issuers)
	}
}
