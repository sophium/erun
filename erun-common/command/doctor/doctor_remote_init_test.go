package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
)

func TestFinishRemoteInitGitCheckoutDryRunCreatesNoDirectory(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "unborn-checkout")
	err := finishRemoteInitGitCheckout(core.Context{DryRun: true}, projectRoot, "", remoteinit.RemoteRepositorySpec{URL: "git@example.test:acme/repo.git"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(projectRoot); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run must not create the project root, got stat err=%v", statErr)
	}
}

// ERUN_ENV_TYPE, not ERUN_REPO_REMOTE, marks a runtime pod.
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
			if got := config.IsInRuntimeEnvironment(lookup); got != tc.want {
				t.Errorf("IsInRuntimeEnvironment() = %v, want %v", got, tc.want)
			}
		})
	}
}
