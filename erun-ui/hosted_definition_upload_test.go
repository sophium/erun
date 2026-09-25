package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	hostedUploadTenant      = "team"
	hostedUploadEnvironment = "dev"
	hostedUploadRowName     = "dev"
	hostedUploadRowType     = "local-agent"
	hostedUploadContext     = "test-context"
)

// hostedUploadRow is the platform row the stub serves. Its name, type and
// Kubernetes context have to agree with the local config or the shared push
// refuses before it uploads anything — which is the refusal working, not a
// fixture problem.
func hostedUploadRow() map[string]any {
	return map[string]any{
		"environmentId":     "env-1",
		"tenantId":          "tenant-1",
		"name":              hostedUploadRowName,
		"type":              hostedUploadRowType,
		"kubernetesContext": hostedUploadContext,
		"status":            "running",
	}
}

// hostedUploadConfig is a local environment already marked as hosted on the
// stub's row, with one portable setting set so a change has something to move.
func hostedUploadConfig(apiHost string) eruncommon.EnvConfig {
	return eruncommon.EnvConfig{
		Name:              hostedUploadEnvironment,
		Type:              eruncommon.EnvironmentTypeLocalAgent,
		KubernetesContext: hostedUploadContext,
		RuntimeVersion:    "1.2.3",
		Hosted: eruncommon.HostedEnvironment{
			APIHost:       apiHost,
			TenantID:      "tenant-1",
			EnvironmentID: "env-1",
		},
	}
}

// hostedUploadPlatform is a stub platform that records every definition upload
// it receives. A test asserts on what actually reached the platform rather
// than on what the desktop intended to send, which is the only way to tell "the
// watcher did not fire" from "the watcher fired and was told to skip".
type hostedUploadPlatform struct {
	*httptest.Server

	mu      sync.Mutex
	uploads []map[string]any
}

func newHostedUploadPlatform(t *testing.T) *hostedUploadPlatform {
	t.Helper()
	platform := &hostedUploadPlatform{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/environments/{environment_id}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(hostedUploadRow())
	})
	mux.HandleFunc("PUT /v1/environments/{environment_id}/definition", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Definition map[string]any `json:"definition"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		platform.mu.Lock()
		platform.uploads = append(platform.uploads, body.Definition)
		revision := len(platform.uploads)
		platform.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": "env-1", "tenantId": "tenant-1",
			"revision": revision, "definition": body.Definition,
		})
	})
	platform.Server = httptest.NewServer(mux)
	t.Cleanup(platform.Close)
	return platform
}

// uploadCount is how many definition writes the platform has received.
func (p *hostedUploadPlatform) uploadCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.uploads)
}

// lastUpload is the payload of the most recent write, or nil when there is
// none.
func (p *hostedUploadPlatform) lastUpload() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.uploads) == 0 {
		return nil
	}
	return p.uploads[len(p.uploads)-1]
}

// isolateHostedUploadConfig points the shared xdg config dir at a temp
// directory for one test. The adrg/xdg package caches its resolved base
// directories at process init, so the env var alone is not enough.
func isolateHostedUploadConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	return home
}

// newHostedUploadApp builds an App whose config tree is a real temporary
// directory and whose tenant platform is a real HTTP stub, so every step the
// watcher drives — the local read, the push, the stamp write — is the
// production one.
func newHostedUploadApp(t *testing.T, platform *hostedUploadPlatform) *App {
	t.Helper()
	secrets := eruncommon.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	if err := eruncommon.SaveERunConfig(eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    testERunAlias,
		Provider: eruncommon.CloudProviderERun,
		Username: "erun",
		ERun:     &eruncommon.ERunProviderConfig{APIURL: platform.URL, ClientID: "test-client", RefreshTokenRef: refreshRef},
	}}}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
	if err := eruncommon.SaveTenantConfig(eruncommon.TenantConfig{Name: hostedUploadTenant}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	return NewApp(erunUIDeps{
		store: eruncommon.ConfigStore{},
		cloudDeps: eruncommon.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(eruncommon.Context, string) (eruncommon.OIDCDiscovery, error) {
				return eruncommon.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(eruncommon.Context, eruncommon.OIDCDiscovery, string, string) (eruncommon.ERunTokens, error) {
				return eruncommon.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
			},
		},
	})
}

// changeHostedUploadConfig edits the environment's config the way an outside
// writer does: read the file, change one setting, write it back. Rebuilding a
// config from a fixture instead would drop the marker's digest, and the digest
// is the very thing half of these tests are about.
func changeHostedUploadConfig(t *testing.T, mutate func(*eruncommon.EnvConfig)) eruncommon.EnvConfig {
	t.Helper()
	config, _, err := eruncommon.LoadEnvConfig(hostedUploadTenant, hostedUploadEnvironment)
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	mutate(&config)
	if err := eruncommon.SaveEnvConfig(hostedUploadTenant, config); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	return config
}

// seededHostedUploadApp is the common setup: an isolated config tree holding
// one hosted environment, and an App over it.
func seededHostedUploadApp(t *testing.T) (*App, *hostedUploadPlatform, string) {
	t.Helper()
	isolateHostedUploadConfig(t)
	platform := newHostedUploadPlatform(t)
	apiHost := platform.URL
	config := hostedUploadConfig(apiHost)
	if err := eruncommon.SaveEnvConfig(hostedUploadTenant, config); err != nil {
		t.Fatalf("seed env config: %v", err)
	}
	return newHostedUploadApp(t, platform), platform, apiHost
}

// TestTheOriginFilterIsWhatTurnsTheWatchersOwnWriteAway isolates the origin
// filter from the digest, which the end-to-end case below cannot.
//
// Both guards stop the ordinary case: a transfer stamps the digest of what it
// sent, so the watcher's reaction to that very write finds nothing to upload
// whether or not the filter is consulted. Staging the one state where the
// digest cannot speak — a marker recording no digest, which asks for an upload
// by definition — is what leaves the filter as the only thing between the
// event and a second upload.
func TestTheOriginFilterIsWhatTurnsTheWatchersOwnWriteAway(t *testing.T) {
	isolateHostedUploadConfig(t)
	platform := newHostedUploadPlatform(t)
	// No digest recorded, so HasUnsentChange() is true and an unfiltered
	// reaction would upload.
	if err := eruncommon.SaveEnvConfig(hostedUploadTenant, hostedUploadConfig(platform.URL)); err != nil {
		t.Fatalf("seed env config: %v", err)
	}
	app := newHostedUploadApp(t, platform)
	target := []definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}}

	// The write a transfer is about to make, recorded by the filter.
	app.definitionWriteOrigin.mark(hostedUploadTenant, hostedUploadEnvironment)
	app.reactToConfigWatchTargets(target)
	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("the watcher fired on this desktop's own write: %d uploads, want 0", got)
	}

	// The same reaction with no mark is an outside change, and it uploads:
	// the refusal above is the filter's, not an environment with nothing to
	// send.
	app.reactToConfigWatchTargets(target)
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("an outside change did not reach the platform: %d uploads, want 1", got)
	}
}

// TestADesktopDefinitionWriteDoesNotReFireTheUpload is the loop this feature
// would otherwise ship, walked one iteration at a time through the real
// transaction: the desktop uploads, which stamps the environment's config
// (that write is what the watcher sees); the watcher then reacts to that very
// write, exactly as its own flush does; and only after that does an outside
// change — `erun cloud set`, `erun init`, a deploy — land. Without the two
// guards the middle step uploads, which stamps again, which the watcher sees
// again. The test above is which guard is doing what.
func TestADesktopDefinitionWriteDoesNotReFireTheUpload(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("the desktop's own upload reached the platform %d times, want 1", got)
	}

	// The watcher's reaction to the config write the upload just made.
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("the watcher fired on this desktop's own definition write: the platform received %d uploads, want 1", got)
	}

	// An outside change, made the way `erun init` / `erun cloud set` make one.
	changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) { config.RuntimeVersion = "9.9.9" })
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 2 {
		t.Fatalf("an outside change did not reach the platform: %d uploads, want 2", got)
	}
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "9.9.9" {
		t.Fatalf("the upload carried runtime version %q, want the changed 9.9.9", got)
	}
}

// TestTheOriginMarkIsClearedWhenTheUploadWritesNothing keeps the filter from
// swallowing a change it did not cause: a transfer that fails before its write
// must not leave a live mark behind, or the next genuine outside change would
// be reported as this desktop's own write and silently dropped.
func TestTheOriginMarkIsClearedWhenTheUploadWritesNothing(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	// A marker naming a row the platform does not serve fails the transfer at
	// its first step — the resolved row is not the one the marker names — so
	// nothing is written and no config event follows.
	changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) { config.Hosted.EnvironmentID = "some-other-row" })

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err == nil {
		t.Fatal("an upload against a row the marker does not name was accepted")
	}
	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("a refused upload reached the platform %d times", got)
	}

	// An outside change after the failure, which is exactly the change a mark
	// left behind by the failed transfer would swallow.
	changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) {
		config.Hosted.EnvironmentID = "env-1"
		config.RuntimeVersion = "4.5.6"
	})
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("a failed transfer's mark swallowed the next outside change: %d uploads, want 1", got)
	}
}

// TestAChangeMadeWhileTheDesktopWasClosedIsSurfacedAndThenUploaded is the
// dirty flag's demonstration. The change is written with no watcher armed,
// which is what "while the desktop was closed" means; it must be visible
// before anything uploads it, and the launch pass must then converge it.
func TestAChangeMadeWhileTheDesktopWasClosedIsSurfacedAndThenUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	// Establish a transfer, so there is a digest to compare against.
	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// An offline change: nothing is watching, and nothing will report it
	// unless the marker itself can tell.
	offline := changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) { config.RuntimeVersion = "7.7.7" })

	marker := hostedEnvironmentToUI(offline)
	if marker == nil {
		t.Fatal("the environment lost its hosted marker")
	}
	if !marker.LocalChange.Available || !marker.LocalChange.Changed {
		t.Fatalf("a change made while the desktop was closed reads as %+v, want available and changed", marker.LocalChange)
	}
	if marker.LocalChange.Describe == "" {
		t.Error("the divergence is not rendered as a sentence the panel can show")
	}

	// Starting the desktop converges it.
	app.catchUpHostedDefinitions()
	if got := platform.uploadCount(); got != 2 {
		t.Fatalf("the launch pass did not upload the offline change: %d uploads, want 2", got)
	}
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "7.7.7" {
		t.Fatalf("the launch pass uploaded runtime version %q, want 7.7.7", got)
	}
}

// TestAHostOwnedChangeIsNotUploaded is the digest guard's negative half: the
// settings that never leave the machine are not a reason to spend an upload,
// and a change to one must not read as divergence in the panel either.
func TestAHostOwnedChangeIsNotUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}
	hostOwned := changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) {
		config.LocalRepoPath = t.TempDir()
		config.RuntimeRunningImage = "registry.example.test/erun-devops:9.9.9"
	})

	if change := hostedEnvironmentToUI(hostOwned).LocalChange; change.Changed {
		t.Fatalf("a host-owned change reads as divergence: %+v", change)
	}
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("a host-owned change was uploaded: %d uploads, want 1", got)
	}
}

// TestAnUnhostedEnvironmentIsNeverUploaded pins the allowlist's outer edge: an
// environment with no marker is not a definition transfer target at all, and a
// config change to one must not reach the platform.
func TestAnUnhostedEnvironmentIsNeverUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	unhosted := hostedUploadConfig(platform.URL)
	unhosted.Name = "plain"
	unhosted.Hosted = eruncommon.HostedEnvironment{}
	if err := eruncommon.SaveEnvConfig(hostedUploadTenant, unhosted); err != nil {
		t.Fatalf("save: %v", err)
	}

	app.autoUploadHostedDefinition(hostedUploadTenant, "plain")

	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("an unhosted environment was uploaded %d times", got)
	}
}

// TestTheWatcherUploadsAnOutsideChangeEndToEnd runs the real fsnotify watcher
// over a real config tree against the stub platform, so the plumbing that
// attributes a file event to an environment and reaches the upload is exercised
// as the desktop actually runs it, rather than only at its decision function.
func TestTheWatcherUploadsAnOutsideChangeEndToEnd(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	app.startConfigWatcher()
	t.Cleanup(app.stopConfigWatcher)

	// A change made the way an outside writer makes one, with the watcher
	// armed — the case that exists precisely because the desktop is running.
	changeHostedUploadConfig(t, func(config *eruncommon.EnvConfig) { config.RuntimeVersion = "3.3.3" })

	waitForUploadCount(t, platform, 1)
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "3.3.3" {
		t.Fatalf("the watcher uploaded runtime version %q, want 3.3.3", got)
	}
}

// waitForUploadCount blocks until the platform has seen want uploads. It waits
// on the observable outcome rather than on a wall clock: the watcher debounces,
// so how long a change takes to arrive is not something a fixed sleep can
// assert, and a sleep that happened to pass would prove nothing.
func waitForUploadCount(t *testing.T, platform *hostedUploadPlatform, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if platform.uploadCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the platform saw %d uploads, want %d", platform.uploadCount(), want)
}

// TestDefinitionWatchTargetForAttributesOnlyAnEnvironmentsOwnConfig is the
// attribution rule the upload path rests on, and it is deliberately narrow.
// The root config.yaml holds CloudContextConfig.AdminToken in plaintext, and
// every live config sits beside its own dated backup.
func TestDefinitionWatchTargetForAttributesOnlyAnEnvironmentsOwnConfig(t *testing.T) {
	isolateHostedUploadConfig(t)
	// The root is the watcher's own root — the directory the config store
	// resolves under — not an unrelated temp tree, or every case below would
	// be rejected for the wrong reason and prove nothing.
	root, err := eruncommon.ERunConfigDir()
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}
	livePath, err := eruncommon.EnvConfigPath(hostedUploadTenant, hostedUploadEnvironment)
	if err != nil {
		t.Fatalf("resolve env config path: %v", err)
	}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"an environment's own config", livePath, true},
		{"the dated backup beside it", livePath + ".2026-09-25.bak", false},
		{"the root config.yaml", filepath.Join(root, "config.yaml"), false},
		{"a tenant's own config.yaml", filepath.Join(root, hostedUploadTenant, "config.yaml"), false},
		{"a directory under an environment", filepath.Join(root, hostedUploadTenant, hostedUploadEnvironment, "leases"), false},
		{"a sibling file of the live config", filepath.Join(root, hostedUploadTenant, hostedUploadEnvironment, "outputs.tar"), false},
		{"a name the config store would refuse", filepath.Join(root, hostedUploadTenant, "..", "escape", "config.yaml"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, ok := definitionWatchTargetFor(root, tc.path)
			if ok != tc.want {
				t.Fatalf("definitionWatchTargetFor(%q) = %v, want %v", tc.path, ok, tc.want)
			}
			if ok && (target.tenant != hostedUploadTenant || target.environment != hostedUploadEnvironment) {
				t.Fatalf("attributed %q to %+v", tc.path, target)
			}
		})
	}
}

// TestTheOriginFilterConsumesAtMostOncePerMark pins the filter's own contract:
// a mark is spent by the first decision, so a burst that straddles a debounce
// window cannot swallow a second change, and an explicit clear drops a mark
// whose write never happened.
func TestTheOriginFilterConsumesAtMostOncePerMark(t *testing.T) {
	var origin definitionWriteOrigin
	if origin.consume("team", "dev") {
		t.Fatal("an unmarked environment read as this desktop's own write")
	}

	origin.mark("team", "dev")
	if !origin.consume("team", "dev") {
		t.Fatal("a marked write did not read as this desktop's own")
	}
	if origin.consume("team", "dev") {
		t.Fatal("one mark served two decisions")
	}

	origin.mark("team", "dev")
	origin.clear("team", "dev")
	if origin.consume("team", "dev") {
		t.Fatal("an explicitly cleared mark still read as this desktop's own")
	}

	// Marks are per environment, not per tenant.
	origin.mark("team", "dev")
	if origin.consume("team", "other") {
		t.Fatal("one environment's mark covered another's write")
	}
	origin.consume("team", "dev")
}

// TestTheDirtyFlagIsStampedOnDisk is the persisted half of the dirty flag: the
// digest has to survive the process, or a change made while the desktop was
// closed is indistinguishable from one made while it was open.
func TestTheDirtyFlagIsStampedOnDisk(t *testing.T) {
	app, _, _ := seededHostedUploadApp(t)
	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(xdg.ConfigHome, "erun", hostedUploadTenant, hostedUploadEnvironment, "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	reloaded, _, err := eruncommon.LoadEnvConfig(hostedUploadTenant, hostedUploadEnvironment)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Hosted.DefinitionDigest == "" {
		t.Fatalf("the upload recorded no digest:\n%s", raw)
	}
	if change := eruncommon.HostedDefinitionLocalChangeFor(reloaded); !change.Available || change.Changed {
		t.Fatalf("a just-uploaded copy reads as %+v", change)
	}
}
