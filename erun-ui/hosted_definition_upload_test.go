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
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

const (
	hostedUploadTenant      = "team"
	hostedUploadEnvironment = "dev"
	hostedUploadRowName     = "dev"
	hostedUploadRowType     = "local-agent"
	hostedUploadContext     = "test-context"
)

// hostedUploadRow must agree with the local config or the push refuses.
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

func hostedUploadConfig(apiHost string) configpkg.EnvConfig {
	return configpkg.EnvConfig{
		Name:              hostedUploadEnvironment,
		Type:              configpkg.EnvironmentTypeLocalAgent,
		KubernetesContext: hostedUploadContext,
		RuntimeVersion:    "1.2.3",
		Hosted: configpkg.HostedEnvironment{
			APIHost:       apiHost,
			TenantID:      "tenant-1",
			EnvironmentID: "env-1",
		},
	}
}

// hostedUploadPlatform records uploads so tests assert on what reached it.
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

func (p *hostedUploadPlatform) uploadCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.uploads)
}

func (p *hostedUploadPlatform) lastUpload() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.uploads) == 0 {
		return nil
	}
	return p.uploads[len(p.uploads)-1]
}

// adrg/xdg caches base dirs at init, so the env var alone is not enough.
func isolateHostedUploadConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	return home
}

func newHostedUploadApp(t *testing.T, platform *hostedUploadPlatform) *App {
	t.Helper()
	secrets := cloud.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	if err := configpkg.SaveERunConfig(configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias:    testERunAlias,
		Provider: configpkg.CloudProviderERun,
		Username: "erun",
		ERun:     &model.ERunProviderConfig{APIURL: platform.URL, ClientID: "test-client", RefreshTokenRef: refreshRef},
	}}}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
	if err := configpkg.SaveTenantConfig(model.TenantConfig{Name: hostedUploadTenant}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	return NewApp(erunUIDeps{
		store: configpkg.ConfigStore{},
		cloudDeps: cloud.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
				return cloud.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
				return cloud.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
			},
		},
	})
}

// Edits in place: rebuilding from a fixture would drop the marker's digest.
func changeHostedUploadConfig(t *testing.T, mutate func(*configpkg.EnvConfig)) configpkg.EnvConfig {
	t.Helper()
	config, _, err := configpkg.LoadEnvConfig(hostedUploadTenant, hostedUploadEnvironment)
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	mutate(&config)
	if err := configpkg.SaveEnvConfig(hostedUploadTenant, config); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	return config
}

func seededHostedUploadApp(t *testing.T) (*App, *hostedUploadPlatform, string) {
	t.Helper()
	isolateHostedUploadConfig(t)
	platform := newHostedUploadPlatform(t)
	apiHost := platform.URL
	config := hostedUploadConfig(apiHost)
	if err := configpkg.SaveEnvConfig(hostedUploadTenant, config); err != nil {
		t.Fatalf("seed env config: %v", err)
	}
	return newHostedUploadApp(t, platform), platform, apiHost
}

// A digest-less marker always asks for an upload, leaving the filter as the
// only guard.
func TestTheOriginFilterIsWhatTurnsTheWatchersOwnWriteAway(t *testing.T) {
	isolateHostedUploadConfig(t)
	platform := newHostedUploadPlatform(t)
	if err := configpkg.SaveEnvConfig(hostedUploadTenant, hostedUploadConfig(platform.URL)); err != nil {
		t.Fatalf("seed env config: %v", err)
	}
	app := newHostedUploadApp(t, platform)
	target := []definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}}

	app.definitionWriteOrigin.mark(hostedUploadTenant, hostedUploadEnvironment)
	app.reactToConfigWatchTargets(target)
	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("the watcher fired on this desktop's own write: %d uploads, want 0", got)
	}

	app.reactToConfigWatchTargets(target)
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("an outside change did not reach the platform: %d uploads, want 1", got)
	}
}

func TestADesktopDefinitionWriteDoesNotReFireTheUpload(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("the desktop's own upload reached the platform %d times, want 1", got)
	}

	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("the watcher fired on this desktop's own definition write: the platform received %d uploads, want 1", got)
	}

	changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) { config.RuntimeVersion = "9.9.9" })
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 2 {
		t.Fatalf("an outside change did not reach the platform: %d uploads, want 2", got)
	}
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "9.9.9" {
		t.Fatalf("the upload carried runtime version %q, want the changed 9.9.9", got)
	}
}

func TestTheOriginMarkIsClearedWhenTheUploadWritesNothing(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	// A marker naming an unknown row fails before any write.
	changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) { config.Hosted.EnvironmentID = "some-other-row" })

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err == nil {
		t.Fatal("an upload against a row the marker does not name was accepted")
	}
	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("a refused upload reached the platform %d times", got)
	}

	changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) {
		config.Hosted.EnvironmentID = "env-1"
		config.RuntimeVersion = "4.5.6"
	})
	app.reactToConfigWatchTargets([]definitionWatchTarget{{tenant: hostedUploadTenant, environment: hostedUploadEnvironment}})
	if got := platform.uploadCount(); got != 1 {
		t.Fatalf("a failed transfer's mark swallowed the next outside change: %d uploads, want 1", got)
	}
}

func TestAChangeMadeWhileTheDesktopWasClosedIsSurfacedAndThenUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}

	offline := changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) { config.RuntimeVersion = "7.7.7" })

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

	app.catchUpHostedDefinitions()
	if got := platform.uploadCount(); got != 2 {
		t.Fatalf("the launch pass did not upload the offline change: %d uploads, want 2", got)
	}
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "7.7.7" {
		t.Fatalf("the launch pass uploaded runtime version %q, want 7.7.7", got)
	}
}

func TestAHostOwnedChangeIsNotUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)

	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}
	hostOwned := changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) {
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

func TestAnUnhostedEnvironmentIsNeverUploaded(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	unhosted := hostedUploadConfig(platform.URL)
	unhosted.Name = "plain"
	unhosted.Hosted = configpkg.HostedEnvironment{}
	if err := configpkg.SaveEnvConfig(hostedUploadTenant, unhosted); err != nil {
		t.Fatalf("save: %v", err)
	}

	app.autoUploadHostedDefinition(hostedUploadTenant, "plain")

	if got := platform.uploadCount(); got != 0 {
		t.Fatalf("an unhosted environment was uploaded %d times", got)
	}
}

func TestTheWatcherUploadsAnOutsideChangeEndToEnd(t *testing.T) {
	app, platform, _ := seededHostedUploadApp(t)
	app.startConfigWatcher()
	t.Cleanup(app.stopConfigWatcher)

	changeHostedUploadConfig(t, func(config *configpkg.EnvConfig) { config.RuntimeVersion = "3.3.3" })

	waitForUploadCount(t, platform, 1)
	if got, _ := platform.lastUpload()["runtimeVersion"].(string); got != "3.3.3" {
		t.Fatalf("the watcher uploaded runtime version %q, want 3.3.3", got)
	}
}

// Waits on the outcome, not a clock: the watcher debounces.
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

// The root config.yaml holds an admin token in plaintext, and every config has
// a dated backup beside it.
func TestDefinitionWatchTargetForAttributesOnlyAnEnvironmentsOwnConfig(t *testing.T) {
	isolateHostedUploadConfig(t)
	// Use the watcher's real root, or every case is rejected for the wrong reason.
	root, err := configpkg.ERunConfigDir()
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}
	livePath, err := configpkg.EnvConfigPath(hostedUploadTenant, hostedUploadEnvironment)
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

func TestTheDirtyFlagIsStampedOnDisk(t *testing.T) {
	app, _, _ := seededHostedUploadApp(t)
	if _, err := app.uploadHostedDefinition(context.Background(), hostedUploadTenant, hostedUploadEnvironment); err != nil {
		t.Fatalf("upload: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(xdg.ConfigHome, "erun", hostedUploadTenant, hostedUploadEnvironment, "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	reloaded, _, err := configpkg.LoadEnvConfig(hostedUploadTenant, hostedUploadEnvironment)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Hosted.DefinitionDigest == "" {
		t.Fatalf("the upload recorded no digest:\n%s", raw)
	}
	if change := configpkg.HostedDefinitionLocalChangeFor(reloaded); !change.Available || change.Changed {
		t.Fatalf("a just-uploaded copy reads as %+v", change)
	}
}
