package eruncommon

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func b64Auth(userPass string) string {
	return base64.StdEncoding.EncodeToString([]byte(userPass))
}

func writeDockerConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write docker config: %v", err)
	}
	return dir
}

func useDockerConfigDir(t *testing.T, dir string) {
	t.Helper()
	prev := dockerConfigDir
	dockerConfigDir = func() string { return dir }
	t.Cleanup(func() { dockerConfigDir = prev })
}

func useCredentialHelper(t *testing.T, fn func(helper, serverURL string) ([]byte, error)) {
	t.Helper()
	prev := runDockerCredentialHelper
	runDockerCredentialHelper = fn
	t.Cleanup(func() { runDockerCredentialHelper = prev })
}

func useGHToken(t *testing.T, fn func(owner string) (string, bool)) {
	t.Helper()
	prev := resolveGHCRTokenViaGH
	resolveGHCRTokenViaGH = fn
	t.Cleanup(func() { resolveGHCRTokenViaGH = prev })
}

func TestResolveRegistryBasicAuthInline(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)
	useCredentialHelper(t, func(string, string) ([]byte, error) {
		t.Fatal("credential helper must not run when no store is configured")
		return nil, nil
	})

	auth, ok := resolveRegistryBasicAuth("ghcr.io")
	if !ok || auth.username != "alice" || auth.secret != "s3cret" {
		t.Fatalf("got %+v ok=%v, want alice/s3cret", auth, ok)
	}
}

func TestResolveRegistryBasicAuthCredsStore(t *testing.T) {
	// osxkeychain-style store: inline auth is empty; the secret lives in the helper.
	dir := writeDockerConfig(t, `{"auths":{"ghcr.io":{}},"credsStore":"osxkeychain"}`)
	useDockerConfigDir(t, dir)
	useCredentialHelper(t, func(helper, serverURL string) ([]byte, error) {
		if helper != "osxkeychain" {
			t.Fatalf("helper = %q, want osxkeychain", helper)
		}
		return []byte(`{"ServerURL":"ghcr.io","Username":"sophium","Secret":"tok"}`), nil
	})

	auth, ok := resolveRegistryBasicAuth("ghcr.io")
	if !ok || auth.username != "sophium" || auth.secret != "tok" {
		t.Fatalf("got %+v ok=%v, want sophium/tok", auth, ok)
	}
}

func TestResolveRegistryBasicAuthCredHelperOverridesStore(t *testing.T) {
	dir := writeDockerConfig(t, `{"credsStore":"store","credHelpers":{"ghcr.io":"perhost"}}`)
	useDockerConfigDir(t, dir)
	var gotHelper string
	useCredentialHelper(t, func(helper, serverURL string) ([]byte, error) {
		gotHelper = helper
		return []byte(`{"Username":"u","Secret":"p"}`), nil
	})

	if _, ok := resolveRegistryBasicAuth("ghcr.io"); !ok {
		t.Fatal("expected credential")
	}
	if gotHelper != "perhost" {
		t.Fatalf("helper = %q, want perhost (per-host override beats credsStore)", gotHelper)
	}
}

func TestResolveRegistryBasicAuthHelperMissFallsBackToInline(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}},"credsStore":"store"}`, b64Auth("bob:pw")))
	useDockerConfigDir(t, dir)
	useCredentialHelper(t, func(string, string) ([]byte, error) {
		return nil, fmt.Errorf("credentials not found in native keychain")
	})

	auth, ok := resolveRegistryBasicAuth("ghcr.io")
	if !ok || auth.username != "bob" || auth.secret != "pw" {
		t.Fatalf("got %+v ok=%v, want bob/pw", auth, ok)
	}
}

func TestResolveRegistryBasicAuthNoConfig(t *testing.T) {
	useDockerConfigDir(t, t.TempDir()) // dir exists but has no config.json
	if auth, ok := resolveRegistryBasicAuth("ghcr.io"); ok {
		t.Fatalf("expected no credential, got %+v", auth)
	}
}

func TestResolveRegistryBasicAuthDockerHubLegacyKey(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"https://index.docker.io/v1/":{"auth":%q}}}`, b64Auth("hubuser:hubpw")))
	useDockerConfigDir(t, dir)
	auth, ok := resolveRegistryBasicAuth("index.docker.io")
	if !ok || auth.username != "hubuser" {
		t.Fatalf("got %+v ok=%v, want hubuser via legacy v1 key", auth, ok)
	}
}

func TestResolveGHCRBasicAuthFallsBackToGH(t *testing.T) {
	useDockerConfigDir(t, t.TempDir()) // no docker cred
	useGHToken(t, func(owner string) (string, bool) {
		if owner != "sophium" {
			t.Fatalf("owner = %q, want sophium", owner)
		}
		return "ghtok", true
	})

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.username != "sophium" || auth.secret != "ghtok" {
		t.Fatalf("got %+v ok=%v, want sophium/ghtok", auth, ok)
	}
}

func TestResolveGHCRBasicAuthPrefersDockerOverGH(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("dockeruser:dockerpw")))
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) {
		t.Fatal("gh fallback must not run when docker has a credential")
		return "", false
	})

	auth, ok := resolveGHCRBasicAuth("sophium")
	if !ok || auth.username != "dockeruser" {
		t.Fatalf("got %+v ok=%v, want docker credential", auth, ok)
	}
}
