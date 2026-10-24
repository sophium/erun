package build

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// writeDockerLoginCaptureStub records the stub docker's argv and stdin.
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

func TestHostedRegistryDockerLoginFailsClearlyWithoutConfiguredAlias(t *testing.T) {
	t.Setenv("ERUN_DOCKER_BIN", failingBinaryPath(t))
	store := erunTestCloudStore{}

	err := hostedRegistryDockerLogin(&store, cloud.CloudDependencies{}, os.Stdout, os.Stderr)
	if err == nil {
		t.Fatal("expected an error when no erun cloud provider alias is configured")
	}
	if !strings.Contains(err.Error(), "erun cloud provider alias") {
		t.Fatalf("error %q does not explain the missing credential", err.Error())
	}
}

func TestHostedRegistryDockerLoginUsesBearerTokenAsPassword(t *testing.T) {
	dir := t.TempDir()
	argsPath := dir + "/args.txt"
	stdinPath := dir + "/stdin.txt"
	writeDockerLoginCaptureStub(t, argsPath, stdinPath)

	provider := erunTestProvider()
	store := erunTestCloudStore{config: config.ERunConfig{CloudProviders: []model.CloudProviderConfig{provider}}}
	secretStore := cloud.NewFileCloudSecretStore(t.TempDir())
	if err := cloud.SaveCachedERunAccessToken(secretStore, provider.Alias, cloud.ERunTokens{AccessToken: "test-bearer-token", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cached access token: %v", err)
	}
	deps := cloud.CloudDependencies{CloudSecretStore: secretStore}

	if err := hostedRegistryDockerLogin(&store, deps, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("hostedRegistryDockerLogin: %v", err)
	}

	gotArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	wantArgs := "login\n" + config.HostedRegistryHost + "\n-u\n" + config.HostedRegistryLoginUsername + "\n--password-stdin\n"
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

func TestDockerRegistryLoginWithHostedRegistryFallsThroughForOtherRegistries(t *testing.T) {
	dir := t.TempDir()
	argsPath := dir + "/args.txt"
	stdinPath := dir + "/stdin.txt"
	writeDockerLoginCaptureStub(t, argsPath, stdinPath)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	// An empty store fails ResolveERunPlatformAlias, so nil proves the hosted branch was skipped.
	store := erunTestCloudStore{}
	login := DockerRegistryLoginWithHostedRegistry(&store, cloud.CloudDependencies{})
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
	cases := []struct {
		registry string
		want     bool
	}{
		{config.HostedRegistryHost, true},
		{"REGISTRY.ERUNPAAS.COM", true},
		{" registry.erunpaas.com ", true},
		{"registry.erunpaas.com/acme", false},
		{"ghcr.io", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isHostedRegistry(tc.registry); got != tc.want {
			t.Errorf("isHostedRegistry(%q) = %v, want %v", tc.registry, got, tc.want)
		}
	}
}
