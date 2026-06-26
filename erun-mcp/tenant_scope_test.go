package erunmcp

import "testing"

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
