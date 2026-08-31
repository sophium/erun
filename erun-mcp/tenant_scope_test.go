package erunmcp

import (
	"strings"
	"testing"
)

func TestScopedTenantEnv(t *testing.T) {
	pod := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "prod"}}
	cases := []struct {
		name             string
		inTenant, inEnv  string
		runtime          RuntimeConfig
		wantTenant, wEnv string
		wantErr          bool
	}{
		{name: "empty input defaults to the pod identity", runtime: pod, wantTenant: "acme", wEnv: "prod"},
		{name: "matching input is accepted", inTenant: "acme", inEnv: "prod", runtime: pod, wantTenant: "acme", wEnv: "prod"},
		{name: "mismatched tenant is refused", inTenant: "evil", inEnv: "prod", runtime: pod, wantErr: true},
		{name: "mismatched environment is refused", inTenant: "acme", inEnv: "staging", runtime: pod, wantErr: true},
		{name: "unconfigured pod falls back to the input", inTenant: "acme", inEnv: "prod", runtime: RuntimeConfig{}, wantTenant: "acme", wEnv: "prod"},
		{name: "unconfigured pod and unconfigured input resolve to nothing, without error", runtime: RuntimeConfig{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant, env, err := scopedTenantEnv(tc.inTenant, tc.inEnv, tc.runtime)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got tenant=%q env=%q", tenant, env)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tenant != tc.wantTenant || env != tc.wEnv {
				t.Fatalf("got tenant=%q env=%q, want %q/%q", tenant, env, tc.wantTenant, tc.wEnv)
			}
		})
	}
}

// TestScopedTenantEnvRefusalNamesBothScopesAndTheRemedy guards the message
// quality this fix depends on: a caller reading the error must be able to
// tell which environment the pod actually serves, which one it asked for
// instead, and what to do about it (call that environment's own edge), not
// just that something was refused.
func TestScopedTenantEnvRefusalNamesBothScopesAndTheRemedy(t *testing.T) {
	pod := RuntimeConfig{Context: RuntimeContext{Tenant: "erun", Environment: "code1"}}
	_, _, err := scopedTenantEnv("erun", "build", pod)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"erun/code1", "erun/build", "own MCP edge"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}
