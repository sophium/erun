package eruncommon

import (
	"os"
	"strings"
	"testing"
	"time"
)

// writeDockerLoginCaptureStub points ERUN_DOCKER_BIN at a script that records
// the argv it was invoked with and whatever it received on stdin, so a test
// can assert exactly what `docker login` was told without a real docker
// daemon or a live registry.
func writeDockerLoginCaptureStub(t *testing.T, argsPath, stdinPath string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/docker-stub"
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + argsPath + "\"\n" +
		"cat > \"" + stdinPath + "\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	t.Setenv("ERUN_DOCKER_BIN", path)
}

// TestHostedRegistryDockerLoginFailsClearlyWithoutConfiguredAlias asserts the
// negative case this issue is about: with no erun cloud provider alias
// configured, no credential can be obtained at all, and the failure must be a
// clear, immediate error rather than an interactive `docker login` left
// waiting on a password nobody can supply.
func TestHostedRegistryDockerLoginFailsClearlyWithoutConfiguredAlias(t *testing.T) {
	t.Setenv("ERUN_DOCKER_BIN", failingBinaryPath(t))
	store := erunTestCloudStore{}

	err := hostedRegistryDockerLogin(&store, CloudDependencies{}, os.Stdout, os.Stderr)
	if err == nil {
		t.Fatal("expected an error when no erun cloud provider alias is configured")
	}
	if !strings.Contains(err.Error(), "erun cloud provider alias") {
		t.Fatalf("error %q does not explain the missing credential", err.Error())
	}
}

// TestHostedRegistryDockerLoginUsesBearerTokenAsPassword asserts the positive
// case: the intended caller — one with a configured, authenticated erun cloud
// provider alias — succeeds, and the credential erun's own docs say to use
// (the tenant's erun-api bearer token) is what actually reaches `docker
// login`'s password, over stdin rather than argv.
func TestHostedRegistryDockerLoginUsesBearerTokenAsPassword(t *testing.T) {
	dir := t.TempDir()
	argsPath := dir + "/args.txt"
	stdinPath := dir + "/stdin.txt"
	writeDockerLoginCaptureStub(t, argsPath, stdinPath)

	provider := erunTestProvider()
	store := erunTestCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{provider}}}
	secretStore := NewFileCloudSecretStore(t.TempDir())
	if err := saveCachedERunAccessToken(secretStore, provider.Alias, ERunTokens{AccessToken: "test-bearer-token", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cached access token: %v", err)
	}
	deps := CloudDependencies{CloudSecretStore: secretStore}

	if err := hostedRegistryDockerLogin(&store, deps, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("hostedRegistryDockerLogin: %v", err)
	}

	gotArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	wantArgs := "login\n" + HostedRegistryHost + "\n-u\n" + HostedRegistryLoginUsername + "\n--password-stdin\n"
	if string(gotArgs) != wantArgs {
		t.Fatalf("docker args = %q, want %q", string(gotArgs), wantArgs)
	}

	gotStdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if string(gotStdin) != "test-bearer-token" {
		t.Fatalf("docker login password (stdin) = %q, want the bearer token", string(gotStdin))
	}
}

// TestDockerRegistryLoginWithHostedRegistryFallsThroughForOtherRegistries
// proves the hosted-registry branch is scoped to registry.erunpaas.com only:
// a login for any other registry must never touch the erun cloud provider
// store — it falls through to DockerRegistryLogin exactly as before.
func TestDockerRegistryLoginWithHostedRegistryFallsThroughForOtherRegistries(t *testing.T) {
	dir := t.TempDir()
	argsPath := dir + "/args.txt"
	stdinPath := dir + "/stdin.txt"
	writeDockerLoginCaptureStub(t, argsPath, stdinPath)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	// An empty store would fail ResolveERunPlatformAlias immediately, so a
	// nil error here proves the hosted branch was never taken.
	store := erunTestCloudStore{}
	login := DockerRegistryLoginWithHostedRegistry(&store, CloudDependencies{})
	if err := login("some-other-registry.example.com", strings.NewReader("whatever-password"), os.Stdout, os.Stderr); err != nil {
		t.Fatalf("login: %v", err)
	}

	gotArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	if want := "login\nsome-other-registry.example.com\n"; string(gotArgs) != want {
		t.Fatalf("docker args = %q, want %q", string(gotArgs), want)
	}
	gotStdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if string(gotStdin) != "whatever-password" {
		t.Fatalf("docker login password (stdin) = %q, want the caller's own stdin", string(gotStdin))
	}
}

func TestIsHostedRegistry(t *testing.T) {
	cases := map[string]bool{
		HostedRegistryHost:           true,
		"REGISTRY.ERUNPAAS.COM":      true,
		" registry.erunpaas.com ":    true,
		"registry.erunpaas.com/acme": false,
		"ghcr.io":                    false,
		"":                           false,
	}
	for registry, want := range cases {
		if got := isHostedRegistry(registry); got != want {
			t.Errorf("isHostedRegistry(%q) = %v, want %v", registry, got, want)
		}
	}
}
