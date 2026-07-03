package eruncommon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// The secret file must be 0600 so other users on the host cannot read it.
	entries, err := os.ReadDir(filepath.Join(dir, "cloud-secrets"))
	mustNoErr(t, err, "readdir")
	if len(entries) != 1 {
		t.Fatalf("expected one secret file, got %d", len(entries))
	}
	info, err := entries[0].Info()
	mustNoErr(t, err, "stat")
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secret file perm = %o, want 600", perm)
	}

	mustNoErr(t, store.DeleteCloudSecret(ref), "delete")
	// Deleting a missing secret is a no-op, not an error.
	mustNoErr(t, store.DeleteCloudSecret(ref), "delete missing")
	if _, err := store.LoadCloudSecret(ref); err == nil {
		t.Fatal("expected load after delete to fail")
	}
}

// mustNoErr fails the test immediately when err is non-nil, keeping
// round-trip tests free of repetitive `if err != nil { t.Fatalf }` blocks.
func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// assertCloudflareAccounts asserts got equals want element-for-element.
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

func TestRenderCloudflareCredentialsSecretRedactsAndQuotes(t *testing.T) {
	manifest := renderCloudflareCredentialsSecret("acme-dev-cloudflare", "acme-dev", `tok"with\quote`)
	if !strings.Contains(manifest, "CLOUDFLARE_API_TOKEN: \"tok\\\"with\\\\quote\"") {
		t.Fatalf("token not safely quoted in manifest:\n%s", manifest)
	}
	// Only the sensitive token belongs in the Secret; the account id rides as a
	// non-secret helm value.
	if strings.Contains(manifest, "CLOUDFLARE_ACCOUNT_ID") {
		t.Fatalf("account id must not be in the secret manifest:\n%s", manifest)
	}
}

func TestCloudProviderTypeFromAlias(t *testing.T) {
	cases := map[string]string{
		"alice+123456789012@aws":  CloudProviderAWS,
		"ci+cf-acct-1@cloudflare": CloudProviderCloudflare,
		"garbage-without-suffix":  CloudProviderAWS,
		"":                        CloudProviderAWS,
	}
	for alias, want := range cases {
		if got := cloudProviderTypeFromAlias(alias); got != want {
			t.Fatalf("cloudProviderTypeFromAlias(%q) = %q, want %q", alias, got, want)
		}
	}
}

func TestResolvedCloudAliasesFoldsLegacyScalarAndMap(t *testing.T) {
	env := EnvConfig{
		CloudProviderAlias: "alice+123456789012@aws",
		CloudProviderAliases: map[string]string{
			CloudProviderCloudflare: "ci+cf-acct-1@cloudflare",
		},
	}
	resolved := env.ResolvedCloudAliases()
	if resolved[CloudProviderAWS] != "alice+123456789012@aws" {
		t.Fatalf("aws slot = %q", resolved[CloudProviderAWS])
	}
	if resolved[CloudProviderCloudflare] != "ci+cf-acct-1@cloudflare" {
		t.Fatalf("cloudflare slot = %q", resolved[CloudProviderCloudflare])
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved aliases, got %d", len(resolved))
	}
}

func TestVerifyCloudflareTokenAtClassifiesResponses(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Fatalf("authorization header = %q", got)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"abc","status":"active"}}`))
		}))
		defer srv.Close()
		info, err := verifyCloudflareTokenAt(srv.URL, "tok")
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if info.ID != "abc" || info.Status != "active" {
			t.Fatalf("info = %+v", info)
		}
	})

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
	// Blank-id entries are dropped.
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
		// One distinct account (a1), blank-account zone dropped.
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

// TestDefaultCloudflareCallsHonorBaseURLSeam proves the subprocess-reachable
// seam: with only the env var set (no in-process CloudDependencies injection),
// the default verifier and accounts resolver hit a mock standing in for the
// Cloudflare API. This is the path a desktop/e2e test exercises through the
// `erun cloud init cloudflare` subprocess.
func TestDefaultCloudflareCallsHonorBaseURLSeam(t *testing.T) {
	gotPaths := make(map[string]bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("authorization header = %q", got)
		}
		gotPaths[r.URL.Path] = true
		switch r.URL.Path {
		case cloudflareTokenVerifyPath:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"abc","status":"active"}}`))
		case cloudflareAccountsPath:
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"a1","name":"Acme"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv(cloudflareAPIBaseURLEnv, srv.URL)

	info, err := defaultVerifyCloudflareToken(Context{}, "tok")
	mustNoErr(t, err, "verify")
	if info.ID != "abc" || info.Status != "active" {
		t.Fatalf("verify info = %+v", info)
	}
	accounts, err := defaultListCloudflareAccounts(Context{}, "tok")
	mustNoErr(t, err, "accounts")
	assertCloudflareAccounts(t, accounts, []CloudflareAccount{{ID: "a1", Name: "Acme"}})

	if !gotPaths[cloudflareTokenVerifyPath] || !gotPaths[cloudflareAccountsPath] {
		t.Fatalf("mock did not receive both Cloudflare paths; got %v", gotPaths)
	}
}

func TestResolveTenantCloudProviderIssuersSkipsCloudflare(t *testing.T) {
	store := stubCloudContextStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{
		{Alias: "alice+123456789012@aws", Provider: CloudProviderAWS, OIDCIssuerURL: "https://issuer.example.com"},
		{Alias: "ci+cf-acct-1@cloudflare", Provider: CloudProviderCloudflare, Cloudflare: &CloudflareProviderConfig{AccountID: "cf-acct-1", TokenRef: "cloudflare/ci+cf-acct-1@cloudflare"}},
	}}}
	tenant := TenantConfig{CloudProviderAliases: []string{"alice+123456789012@aws", "ci+cf-acct-1@cloudflare"}}

	issuers, err := ResolveTenantCloudProviderIssuers(store, tenant)
	if err != nil {
		t.Fatalf("resolve issuers: %v", err)
	}
	if len(issuers) != 1 || issuers[0] != "https://issuer.example.com" {
		t.Fatalf("issuers = %v, want only the AWS issuer (Cloudflare has no OIDC)", issuers)
	}
}
