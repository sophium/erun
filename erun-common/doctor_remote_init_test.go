package eruncommon

import "testing"

// TestIsInRuntimeEnvironment pins the fix for the bug where a local-agent
// pod's hostPath-mounted worktree (ERUN_REPO_REMOTE=false) made this function
// report "not in a runtime pod" while running inside one. ERUN_ENV_TYPE is
// the signal now: set by the chart on every pod it renders, to a value other
// than host only for a pod.
func TestIsInRuntimeEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "local-agent pod",
			env: map[string]string{
				"ERUN_ENV_TYPE":    "local-agent",
				"ERUN_REPO_REMOTE": "false",
				"ERUN_TENANT":      "frs",
				"ERUN_ENVIRONMENT": "local",
			},
			want: true,
		},
		{
			name: "remote-agent pod",
			env: map[string]string{
				"ERUN_ENV_TYPE":    "remote-agent",
				"ERUN_REPO_REMOTE": "true",
				"ERUN_TENANT":      "erun",
				"ERUN_ENVIRONMENT": "build",
			},
			want: true,
		},
		{
			name: "runtime pod",
			env: map[string]string{
				"ERUN_ENV_TYPE":    "runtime",
				"ERUN_REPO_REMOTE": "true",
			},
			want: true,
		},
		{
			name: "host laptop",
			env:  map[string]string{},
			want: false,
		},
		{
			name: "legacy chart predating ERUN_ENV_TYPE, remote-agent",
			env: map[string]string{
				"ERUN_REPO_REMOTE": "true",
			},
			want: true,
		},
		{
			name: "legacy chart predating ERUN_ENV_TYPE, local-agent",
			env: map[string]string{
				"ERUN_REPO_REMOTE": "false",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) string { return tc.env[key] }
			if got := IsInRuntimeEnvironment(lookup); got != tc.want {
				t.Errorf("IsInRuntimeEnvironment() = %v, want %v", got, tc.want)
			}
		})
	}
}
