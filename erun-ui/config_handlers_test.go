package main

import (
	"errors"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestLoadTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.LoadTenantConfig("   ")
	if err == nil || !errors.Is(err, uimodel.ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "loading tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

func TestSaveTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.SaveTenantConfig(uiTenantConfig{Name: "  "})
	if err == nil || !errors.Is(err, uimodel.ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "saving tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

// Saving writes the whole catalog back, so a conversion dropping the declaration would delete it.
func TestOpenRouterCatalogRoundTripsTheReasoningEchoDeclaration(t *testing.T) {
	config := &configpkg.OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		DefaultModel: "anthropic/claude-fable-5.1",
		Models: []configpkg.OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 262144, RequiresReasoningEcho: true},
			{ID: "anthropic/claude-fable-5.1", Context: 200000},
		},
	}

	roundTripped := openRouterConfigFromUI(openRouterConfigToUI(config))
	if roundTripped == nil {
		t.Fatal("expected a catalog back")
	}
	if len(roundTripped.Models) != 2 {
		t.Fatalf("models = %+v, want both listings", roundTripped.Models)
	}
	if !roundTripped.Models[0].RequiresReasoningEcho {
		t.Fatalf("the declaration was dropped on save: %+v", roundTripped.Models[0])
	}
	if roundTripped.Models[1].RequiresReasoningEcho {
		t.Fatalf("a driveable listing came back declared: %+v", roundTripped.Models[1])
	}
	if got := roundTripped.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want the driveable listing", got)
	}
	if ids := roundTripped.ModelIDs(); len(ids) != 1 || ids[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want the declared listing omitted", ids)
	}
}

// Launches drop ids that are not plain model tokens, so saving one must be refused.
func TestSavingTheGatewayCatalogRefusesAModelIDNoLaunchWouldUse(t *testing.T) {
	stored := &configpkg.OpenRouterConfig{BaseURL: "https://gateway.example.test", Models: []configpkg.OpenRouterModel{{ID: "acme/coder"}}}
	cases := []struct {
		name    string
		catalog uiOpenRouterConfig
		want    string
	}{
		{"flag-shaped model", uiOpenRouterConfig{BaseURL: "https://gateway.example.test", Models: []uiOpenRouterModel{{ID: "acme/coder"}, {ID: "--dangerously-skip-permissions"}}}, `invalid model "--dangerously-skip-permissions"`},
		{"shell syntax in a model", uiOpenRouterConfig{BaseURL: "https://gateway.example.test", Models: []uiOpenRouterModel{{ID: "acme/coder; rm -rf ~"}}}, `invalid model "acme/coder; rm -rf ~"`},
		{"shell syntax in the default", uiOpenRouterConfig{BaseURL: "https://gateway.example.test", DefaultModel: "$(id)", Models: []uiOpenRouterModel{{ID: "acme/coder"}}}, `default invalid model "$(id)"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := stubUIStore{config: &configpkg.ERunConfig{OpenRouter: stored}}
			app := NewApp(erunUIDeps{store: store})
			_, err := app.SaveERunConfig(uiERunConfig{OpenRouter: &tc.catalog})
			if err == nil || !strings.Contains(err.Error(), "cannot save the gateway catalog: "+tc.want) {
				t.Fatalf("expected the save refused naming the id (%s), got %v", tc.want, err)
			}
			if store.config.OpenRouter != stored {
				t.Fatalf("a refused save replaced the catalog with %+v", store.config.OpenRouter)
			}
		})
	}

	store := stubUIStore{config: &configpkg.ERunConfig{}}
	app := NewApp(erunUIDeps{store: store})
	_, err := app.SaveERunConfig(uiERunConfig{OpenRouter: &uiOpenRouterConfig{
		BaseURL:      "https://gateway.example.test",
		DefaultModel: " acme/coder ",
		Models:       []uiOpenRouterModel{{ID: " acme/coder "}, {ID: "vendor/model:free"}, {ID: "  "}},
	}})
	if err != nil {
		t.Fatalf("expected plain model ids to save, got %v", err)
	}
	if got := store.config.OpenRouter.ModelIDs(); len(got) != 2 || got[0] != "acme/coder" || got[1] != "vendor/model:free" {
		t.Fatalf("expected both ids stored trimmed, got %q", got)
	}
}

func TestTheSaveCheckAgreesWithWhatTheCatalogOffers(t *testing.T) {
	for _, id := range []string{"opus", "acme/coder", "vendor/model:free", "a.b_c-d", "-p", "--model", "a b", "a;b", "$(id)", "a'b", "a\"b", "a|b", "ü", "a\nb", ".hidden", "/abs", ":tag"} {
		offered := len((&configpkg.OpenRouterConfig{Models: []configpkg.OpenRouterModel{{ID: id}}}).ModelIDs()) == 1
		if accepted := validateGatewayCatalogModelIDs(&configpkg.OpenRouterConfig{Models: []configpkg.OpenRouterModel{{ID: id}}}) == nil; accepted != offered {
			t.Fatalf("id %q: the save accepts=%v but the catalog offers=%v", id, accepted, offered)
		}
	}
}
