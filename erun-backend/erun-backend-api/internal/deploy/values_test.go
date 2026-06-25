package deploy

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func TestRuntimeValuesSetsMCPAuthFromInjectedKey(t *testing.T) {
	ctxRow := model.Context{Name: "prod", Provider: "aws", Region: "eu-west-1", InstanceID: "i-123"}
	values := runtimeValues("acme", "prod", ctxRow, "ghcr.io/sophium", "",
		"file:///etc/erun/mcp-auth/desktopid.pub", "erun-mcp:acme/prod", "acme-devops-mcp-auth")

	mcpAuth, ok := values["mcpAuth"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcpAuth to be set when an issuer is given, got %#v", values["mcpAuth"])
	}
	if mcpAuth["enabled"] != true {
		t.Errorf("mcpAuth.enabled = %#v, want true", mcpAuth["enabled"])
	}
	if mcpAuth["issuer"] != "file:///etc/erun/mcp-auth/desktopid.pub" {
		t.Errorf("mcpAuth.issuer = %#v, want the file:// signing issuer", mcpAuth["issuer"])
	}
	if mcpAuth["audience"] != "erun-mcp:acme/prod" {
		t.Errorf("mcpAuth.audience = %#v, want the per-env audience", mcpAuth["audience"])
	}
	if mcpAuth["secretName"] != "acme-devops-mcp-auth" {
		t.Errorf("mcpAuth.secretName = %#v, want the injected-key secret", mcpAuth["secretName"])
	}
}

func TestRuntimeValuesOmitsMCPAuthWithoutIssuer(t *testing.T) {
	ctxRow := model.Context{Name: "prod", Provider: "aws", Region: "eu-west-1", InstanceID: "i-123"}
	values := runtimeValues("acme", "prod", ctxRow, "ghcr.io/sophium", "", "", "", "")

	if _, present := values["mcpAuth"]; present {
		t.Errorf("expected no mcpAuth key when no issuer is resolved (edge stays loopback-only), got %#v", values["mcpAuth"])
	}
}
