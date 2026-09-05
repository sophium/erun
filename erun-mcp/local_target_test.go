package erunmcp

import (
	"context"
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
// The error itself must not be the bare "tenant and environment are
// required" dead end that made exec_agent's failure unrecoverable: it must
// name what is missing and what the caller can do about it.
func TestResolveLocalTargetRequiresBothWhenTheServerHasNoScope(t *testing.T) {
	runtime := RuntimeConfig{}
	_, _, err := resolveLocalTarget(runtime, "", "")
	if err == nil {
		t.Fatal("expected an error when neither the server nor the caller names a target")
	}
	assertNamesMissingAndRecovery(t, err, "tenant/environment")

	_, _, err = resolveLocalTarget(runtime, "tenant-a", "")
	if err == nil {
		t.Fatal("expected an error when the environment is missing entirely")
	}
	assertNamesMissingAndRecovery(t, err, "environment")
}

// assertNamesMissingAndRecovery insists a target-resolution error names the
// missing subject and a concrete recovery ("pass ... explicitly"), not just
// that an error occurred -- a bare "tenant and environment are required"
// would pass a plain err != nil check but is exactly the dead end this
// guards against.
func assertNamesMissingAndRecovery(t *testing.T, err error, wantSubject string) {
	t.Helper()
	if err.Error() == "tenant and environment are required" || err.Error() == "tenant is required" || err.Error() == "environment is required" {
		t.Fatalf("error regressed to a bare required-input message with no recovery: %v", err)
	}
	if !strings.Contains(err.Error(), wantSubject) {
		t.Errorf("error should name %q, got: %v", wantSubject, err)
	}
	if !strings.Contains(err.Error(), "pass") {
		t.Errorf("error should tell the caller to pass the missing value explicitly, got: %v", err)
	}
}

// delete's own tenant/environment resolution (scopedTenantEnv, not
// resolveLocalTarget) hit the identical bare "tenant and environment are
// required" dead end when neither the server nor the caller named a target.
// It carries the same fix and deserves the same regression coverage.
func TestDeleteRefusesWithNamedRecoveryWhenNeitherSideNamesATarget(t *testing.T) {
	runtime := RuntimeConfig{}
	_, _, err := deleteTool(runtime)(context.Background(), nil, DeleteInput{Preview: true})
	if err == nil {
		t.Fatal("expected an error when neither the server nor the caller names a target")
	}
	assertNamesMissingAndRecovery(t, err, "tenant/environment")
}
