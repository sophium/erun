package erunmcp

import (
	"strings"
	"testing"
)

func TestResolveLocalTargetRefusesAForeignEnvironment(t *testing.T) {
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"foreign tenant", "tenant-b", "dev"},
		{"foreign environment", "tenant-a", "prod"},
		{"foreign both", "tenant-b", "prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveLocalTarget(runtime, tc.tenant, tc.environment)
			if err == nil {
				t.Fatalf("expected %s/%s to be refused by a server serving tenant-a/dev", tc.tenant, tc.environment)
			}
			// The message must name both scopes, or the caller cannot work out which
			// edge to call instead.
			if !strings.Contains(err.Error(), "tenant-a/dev") {
				t.Errorf("error should name the server's own scope, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.tenant+"/"+tc.environment) {
				t.Errorf("error should name the requested scope, got: %v", err)
			}
		})
	}
}

func TestResolveLocalTargetAcceptsItsOwnScope(t *testing.T) {
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"omitted", "", ""},
		{"restated", "tenant-a", "dev"},
		{"tenant only", "tenant-a", ""},
		{"environment only", "", "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant, environment, err := resolveLocalTarget(runtime, tc.tenant, tc.environment)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tenant != "tenant-a" || environment != "dev" {
				t.Errorf("got %s/%s, want tenant-a/dev", tenant, environment)
			}
		})
	}
}

// A server with no scope of its own still needs both values from the caller.
func TestResolveLocalTargetRequiresBothWhenTheServerHasNoScope(t *testing.T) {
	runtime := RuntimeConfig{}
	if _, _, err := resolveLocalTarget(runtime, "", ""); err == nil {
		t.Error("expected an error when neither the server nor the caller names a target")
	}
	if _, _, err := resolveLocalTarget(runtime, "tenant-a", ""); err == nil {
		t.Error("expected an error when the environment is missing entirely")
	}
}
