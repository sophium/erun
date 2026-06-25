package deploy

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func TestRuntimeValuesSetsMCPAuthFromOIDCIssuer(t *testing.T) {
	ctxRow := model.Context{Name: "prod", Provider: "aws", Region: "eu-west-1", InstanceID: "i-123"}
	values := runtimeValues("acme", "prod", ctxRow, "ghcr.io/sophium", "", "https://issuer.example", "erun-mcp:acme/prod")

	mcpAuth, ok := values["mcpAuth"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcpAuth to be set when an issuer is given, got %#v", values["mcpAuth"])
	}
	if mcpAuth["enabled"] != true {
		t.Errorf("mcpAuth.enabled = %#v, want true", mcpAuth["enabled"])
	}
	if mcpAuth["issuer"] != "https://issuer.example" {
		t.Errorf("mcpAuth.issuer = %#v, want the OIDC issuer", mcpAuth["issuer"])
	}
	if mcpAuth["audience"] != "erun-mcp:acme/prod" {
		t.Errorf("mcpAuth.audience = %#v, want the per-env audience", mcpAuth["audience"])
	}
}

func TestRuntimeValuesOmitsMCPAuthWithoutIssuer(t *testing.T) {
	ctxRow := model.Context{Name: "prod", Provider: "aws", Region: "eu-west-1", InstanceID: "i-123"}
	values := runtimeValues("acme", "prod", ctxRow, "ghcr.io/sophium", "", "", "")

	if _, present := values["mcpAuth"]; present {
		t.Errorf("expected no mcpAuth key when no issuer is resolved (edge stays loopback-only), got %#v", values["mcpAuth"])
	}
}
