package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-integration/internal/env"
)

// SeedTenantEnvWithSignedInERunPlatformAlias seeds a tenant/environment exactly
// as SeedTenantEnv does, plus the invoking host's own signed-in erun platform
// alias: the root config naming it (with the stored-session ref the alias
// carries) and the refresh token that ref resolves to. That pair is what makes
// a host a credential source -- an alias with no stored session is deliberately
// "nothing to give" -- so it is the shape any scenario about provisioning a
// platform alias into an environment has to start from.
//
// The store itself writes the token, so the basename it lands under is the one
// production will later look for rather than a second copy of that hash.
func SeedTenantEnvWithSignedInERunPlatformAlias(t testing.TB, setup env.Setup, tenant, environment, alias string) {
	t.Helper()
	SeedTenantEnv(t, setup, tenant, environment)
	SeedSignedInERunPlatformAlias(t, setup, tenant, alias, "https://api.example.test")
}

// SeedSignedInERunPlatformAlias makes the invoking host a signed-in credential
// source, without seeding a tenant/environment tree: the root config naming
// alias on the erun platform at apiURL (with the stored-session ref the alias
// carries) and the refresh token that ref resolves to.
//
// It takes the platform's URL because the two halves have to agree: a scenario
// that drives what the host does with that alias -- the machine-identity
// provisioning `erun init` asks for over it, or a platform call made through it
// -- points both at the stub server it seeded, while a scenario only asserting
// that a credential was delivered keeps the fixed host.
func SeedSignedInERunPlatformAlias(t testing.TB, setup env.Setup, tenant, alias, apiURL string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	// The stored-session reference travels with the alias, exactly as it does in
	// a real signed-in config: the alias is inert without it.
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
	store := cloud.NewFileCloudSecretStore(filepath.Join(root, "cloud-secrets"))
	if err := store.SaveCloudSecret(ref, "refresh-token-value"); err != nil {
		t.Fatalf("save host refresh token: %v", err)
	}
}

// SeedMachineIdentityCredential stores clientSecret under the ref an alias's
// machine-identity entry names, so a scenario can put a host or an environment
// on a credential without an `erun init` having minted one. The ref is derived
// the way erun-common derives it, so the two cannot drift.
func SeedMachineIdentityCredential(t testing.TB, setup env.Setup, alias, clientSecret string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	store := cloud.NewFileCloudSecretStore(filepath.Join(root, "cloud-secrets"))
	if err := store.SaveCloudSecret("erun/clientsecret/"+alias, clientSecret); err != nil {
		t.Fatalf("save machine identity client secret: %v", err)
	}
}

// SeedMachineIdentityAlias writes the root config naming an erun platform alias
// that authenticates as a machine identity rather than as a delegated human
// session: a client-secret reference and no refresh-token reference. It is the
// shape a pod carries once `erun init` has provisioned its own identity.
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

// SeedHostedRuntimeTenantEnv writes a runtime-type env that is marked as hosted
// on a platform row, which is the state `erun platform env push` / `pull` act
// on. The marker's api host, tenant id and environment id are the caller's, so
// a scenario can point it at the stub server it seeded and prove the marker is
// compared rather than trusted.
//
// The env carries values in both classes on purpose: runtimeversion and
// deploy.timeout travel, while the repo path, the local port range and the
// image pull secret name must survive a pull untouched.
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

	// The root config.yaml is deliberately left alone: a scenario that drives
	// `erun platform` seeds the platform alias there, and overwriting it would
	// leave the command with no alias to resolve. Both verbs name the tenant
	// explicitly, so nothing here depends on a default tenant.
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
			// An unmodelled key: nothing in this binary declares it, so it is
			// exactly what marshalConfigPreservingUnknownFields exists to keep. A
			// pull must not drop it.
			"futureunmodelledkey: keep-me\n"+
			"hosted:\n"+
			"  apihost: "+apiHost+"\n"+
			"  tenantid: "+tenantID+"\n"+
			"  environmentid: "+environmentID+"\n"+
			"  definitionrevision: "+strconv.Itoa(revision)+"\n",
	)
}
