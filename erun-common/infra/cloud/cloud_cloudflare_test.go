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

func assertCloudflareAccounts(t *testing.T, got, want []CloudflareAccount) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("accounts = %+v, want %+v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("account[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestRedactSecretPresence(t *testing.T) {
	if got := redactSecretPresence("super-secret"); got != "<redacted>" {
		t.Fatalf("present = %q, want <redacted>", got)
	}
	if got := redactSecretPresence("   "); got != "<empty>" {
		t.Fatalf("blank = %q, want <empty>", got)
	}
}

func TestCloudProviderTypeFromAlias(t *testing.T) {
	cases := map[string]string{
		"alice+123456789012@aws":  config.CloudProviderAWS,
		"ci+cf-acct-1@cloudflare": config.CloudProviderCloudflare,
		"garbage-without-suffix":  config.CloudProviderAWS,
		"":                        config.CloudProviderAWS,
	}
	for alias, want := range cases {
		if got := config.CloudProviderTypeFromAlias(alias); got != want {
			t.Fatalf("cloudProviderTypeFromAlias(%q) = %q, want %q", alias, got, want)
		}
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

func TestListCloudflareAccountsAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("authorization header = %q", got)
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"a1","name":"Acme"},{"id":"a2","name":"Beta"},{"id":"","name":"skip-blank"}]}`))
	}))
	defer srv.Close()
	accounts, err := listCloudflareAccountsAt(srv.URL, "tok")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(accounts) != 2 || accounts[0].ID != "a1" || accounts[1].Name != "Beta" {
		t.Fatalf("accounts = %+v", accounts)
	}

	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
	}))
	defer rejected.Close()
	if _, err := listCloudflareAccountsAt(rejected.URL, "tok"); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("expected rejection error, got %v", err)
	}
}

func TestResolveCloudflareAccountsViaZones(t *testing.T) {
	t.Run("dedupes account across zones", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Fatalf("authorization header = %q", got)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[
				{"id":"z1","account":{"id":"a1","name":"Acme"}},
				{"id":"z2","account":{"id":"a1","name":"Acme"}},
				{"id":"z3","account":{"id":"","name":"skip-blank"}}
			]}`))
		}))
		defer srv.Close()
		accounts, err := resolveCloudflareAccountsViaZones(srv.URL, "tok")
		mustNoErr(t, err, "resolve")
		assertCloudflareAccounts(t, accounts, []CloudflareAccount{{ID: "a1", Name: "Acme"}})
	})

	t.Run("returns each distinct account", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"result":[
				{"id":"z1","account":{"id":"a1","name":"Acme"}},
				{"id":"z2","account":{"id":"a2","name":"Beta"}}
			]}`))
		}))
		defer srv.Close()
		accounts, err := resolveCloudflareAccountsViaZones(srv.URL, "tok")
		mustNoErr(t, err, "resolve")
		assertCloudflareAccounts(t, accounts, []CloudflareAccount{{ID: "a1", Name: "Acme"}, {ID: "a2", Name: "Beta"}})
	})

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
