package cloud

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestFileCloudSecretStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCloudSecretStore(filepath.Join(dir, "cloud-secrets"))
	const ref = "cloudflare/ci+acct@cloudflare"

	mustNoErr(t, store.SaveCloudSecret(ref, "tok-value"), "save")
	got, err := store.LoadCloudSecret(ref)
	mustNoErr(t, err, "load")
	if got != "tok-value" {
		t.Fatalf("load = %q, want tok-value", got)
	}

	// Windows has no POSIX mode bits, so a 0600 file reports 0666 there.
	entries, err := os.ReadDir(filepath.Join(dir, "cloud-secrets"))
	mustNoErr(t, err, "readdir")
	if len(entries) != 1 {
		t.Fatalf("expected one secret file, got %d", len(entries))
	}
	info, err := entries[0].Info()
	mustNoErr(t, err, "stat")
	wantPerm := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		wantPerm = 0o666
	}
	if perm := info.Mode().Perm(); perm != wantPerm {
		t.Fatalf("secret file perm = %o, want %o", perm, wantPerm)
	}

	mustNoErr(t, store.DeleteCloudSecret(ref), "delete")
	mustNoErr(t, store.DeleteCloudSecret(ref), "delete missing")
	if _, err := store.LoadCloudSecret(ref); err == nil {
		t.Fatal("expected load after delete to fail")
	}
}

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

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

func TestVerifyCloudflareTokenAtClassifiesResponses(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"Invalid API Token"}]}`))
		}))
		defer srv.Close()
		if _, err := verifyCloudflareTokenAt(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "Invalid API Token") {
			t.Fatalf("expected rejection error, got %v", err)
		}
	})

	t.Run("disabled-token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"abc","status":"disabled"}}`))
		}))
		defer srv.Close()
		if _, err := verifyCloudflareTokenAt(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("expected disabled error, got %v", err)
		}
	})
}

func TestListCloudflareAccountsAtSurfacesRejection(t *testing.T) {
	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
	}))
	defer rejected.Close()
	if _, err := listCloudflareAccountsAt(rejected.URL, "tok"); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("expected rejection error, got %v", err)
	}
}

func TestResolveCloudflareAccountsViaZones(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
		}))
		defer srv.Close()
		if _, err := resolveCloudflareAccountsViaZones(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "bad token") {
			t.Fatalf("expected rejection error, got %v", err)
		}
	})
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
