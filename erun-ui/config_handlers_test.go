package main

import (
	"errors"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestLoadTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.LoadTenantConfig("   ")
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "loading tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

func TestSaveTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.SaveTenantConfig(uiTenantConfig{Name: "  "})
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "saving tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

// TestOpenRouterCatalogRoundTripsTheReasoningEchoDeclaration pins the editor's
// own hole in this contract: saving the catalog writes the whole list back, so a
// conversion that did not carry the declaration would delete it on the first
// save from the desktop and quietly return the model to the selectable set the
// declaration exists to keep it out of.
func TestOpenRouterCatalogRoundTripsTheReasoningEchoDeclaration(t *testing.T) {
	config := &eruncommon.OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		DefaultModel: "anthropic/claude-fable-5.1",
		Models: []eruncommon.OpenRouterModel{
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
	// The declaration is what the lanes act on, so the id it applies to must not
	// resolve as the environment's model or be offered for selection.
	if got := roundTripped.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want the driveable listing", got)
	}
	if ids := roundTripped.ModelIDs(); len(ids) != 1 || ids[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want the declared listing omitted", ids)
	}
}
