package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-integration/internal/env"
)

// SeedTenantEnvWithSignedInERunPlatformAlias is SeedTenantEnv plus
// SeedSignedInERunPlatformAlias at a fixed API host.
func SeedTenantEnvWithSignedInERunPlatformAlias(t testing.TB, setup env.Setup, tenant, environment, alias string) {
	t.Helper()
	SeedTenantEnv(t, setup, tenant, environment)
	SeedSignedInERunPlatformAlias(t, setup, tenant, alias, "https://api.example.test")
}

// SeedSignedInERunPlatformAlias writes a root config naming alias at apiURL
// with a stored refresh token, making the host a credential source.
func SeedSignedInERunPlatformAlias(t testing.TB, setup env.Setup, tenant, alias, apiURL string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	// The alias is inert without its stored-session ref.
	ref := "erun/refresh/" + alias
	mustWrite(t, filepath.Join(root, "config.yaml"),
		"defaulttenant: "+tenant+"\n"+
			"cloudproviders:\n"+
			"  - alias: "+alias+"\n"+
			"    provider: erun\n"+
			"    oidcissuerurl: "+apiURL+"\n"+
			"    erun:\n"+
			"      apiurl: "+apiURL+"\n"+
			"      clientid: cli-test-client\n"+
			"      refreshtokenref: "+ref+"\n",
	)
	store := secretstore.NewFileCloudSecretStore(filepath.Join(root, "cloud-secrets"))
	if err := store.SaveCloudSecret(ref, "refresh-token-value"); err != nil {
		t.Fatalf("save host refresh token: %v", err)
	}
}

// SeedMachineIdentityCredential stores clientSecret under the alias's
// machine-identity ref.
func SeedMachineIdentityCredential(t testing.TB, setup env.Setup, alias, clientSecret string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	store := secretstore.NewFileCloudSecretStore(filepath.Join(root, "cloud-secrets"))
	if err := store.SaveCloudSecret("erun/clientsecret/"+alias, clientSecret); err != nil {
		t.Fatalf("save machine identity client secret: %v", err)
	}
}

// SeedMachineIdentityAlias writes a root config naming an alias that
// authenticates as a machine identity (client secret, no refresh token).
func SeedMachineIdentityAlias(t testing.TB, setup env.Setup, tenant, alias, apiURL, clientID, clientSecret string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"),
		"defaulttenant: "+tenant+"\n"+
			"cloudproviders:\n"+
			"  - alias: "+alias+"\n"+
			"    provider: erun\n"+
			"    oidcissuerurl: "+apiURL+"\n"+
			"    erun:\n"+
			"      apiurl: "+apiURL+"\n"+
			"      clientid: "+clientID+"\n"+
			"      clientsecretref: erun/clientsecret/"+alias+"\n",
	)
	SeedMachineIdentityCredential(t, setup, alias, clientSecret)
}

// SeedHostedRuntimeTenantEnv writes a runtime env marked as hosted on the given
// platform row. It mixes values a pull overwrites (runtimeversion,
// deploy.timeout) with host-owned ones that must survive it.
func SeedHostedRuntimeTenantEnv(t testing.TB, setup env.Setup, tenant, environment, apiHost, tenantID, environmentID string, revision int) {
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

	// Leave the root config alone: it may hold the scenario's platform alias.
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"type: runtime\n"+
			"kubernetescontext: test-context\n"+
			"localrepopath: "+repoPath+"\n"+
			"localportrangestart: 17100\n"+
			"runtimeversion: 1.0.0\n"+
			"imagepullsecrets:\n"+
			"  - host-owned-pull-secret\n"+
			"deploy:\n"+
			"  timeout: 7m\n"+
			"  components:\n"+
			"    - host-owned-component\n"+
			// Unmodelled key that a pull must preserve.
			"futureunmodelledkey: keep-me\n"+
			"hosted:\n"+
			"  apihost: "+apiHost+"\n"+
			"  tenantid: "+tenantID+"\n"+
			"  environmentid: "+environmentID+"\n"+
			"  definitionrevision: "+strconv.Itoa(revision)+"\n",
	)
}
