package eruncommon

import "testing"

// TestWithInjectedRuntimeType covers the fallback that keeps a pod's own
// environment type from silently reading as unresolved. An unsynced in-pod env
// config carries no `type`, and an unresolved type makes UsesDindSidecar()
// answer "no erun-dind sidecar" rather than "cannot tell" -- the exact silent
// under-report RuntimeUsage.ExcludesBuilds exists to prevent.
func TestWithInjectedRuntimeType(t *testing.T) {
	cases := []struct {
		name        string
		onDisk      EnvironmentType
		injectedFor [2]string // tenant, environment the injected identity names
		resolving   [2]string // tenant, environment being resolved
		want        EnvironmentType
	}{
		{
			name:        "unsynced config takes the injected type for its own environment",
			onDisk:      "",
			injectedFor: [2]string{"tenant-a", "dev"},
			resolving:   [2]string{"tenant-a", "dev"},
			want:        EnvironmentTypeRemoteAgent,
		},
		{
			name:        "an already-resolved type is never overwritten",
			onDisk:      EnvironmentTypeRuntime,
			injectedFor: [2]string{"tenant-a", "dev"},
			resolving:   [2]string{"tenant-a", "dev"},
			want:        EnvironmentTypeRuntime,
		},
		{
			name:        "another environment is untouched",
			onDisk:      "",
			injectedFor: [2]string{"tenant-a", "dev"},
			resolving:   [2]string{"tenant-a", "prod"},
			want:        "",
		},
		{
			name:        "no injected identity leaves the type unresolved",
			onDisk:      "",
			injectedFor: [2]string{"", ""},
			resolving:   [2]string{"tenant-a", "dev"},
			want:        "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ERUN_TENANT", tc.injectedFor[0])
			t.Setenv("ERUN_ENVIRONMENT", tc.injectedFor[1])
			t.Setenv("ERUN_ENV_TYPE", string(EnvironmentTypeRemoteAgent))

			got := withInjectedRuntimeType(tc.resolving[0], tc.resolving[1], EnvConfig{Type: tc.onDisk})
			if got.Type != tc.want {
				t.Fatalf("Type = %q, want %q", got.Type, tc.want)
			}
		})
	}
}
