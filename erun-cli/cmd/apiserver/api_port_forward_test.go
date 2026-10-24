package apiserver

import (
	"regexp"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestKubectlAPIPortForwardArgsResolvesTheEnvironmentsOwnServicePort(t *testing.T) {
	// Injected ERUN_* ports outrank the config-derived block.
	for _, name := range []string{"ERUN_TENANT", "ERUN_ENVIRONMENT", "ERUN_MCP_PORT", "ERUN_SSHD_PORT"} {
		t.Setenv(name, "")
	}

	for _, tc := range []struct {
		name      string
		rangeSt   int
		wantLocal int
		wantMap   string
	}{
		{
			name:      "the canonical-block env still forwards to 17033",
			rangeSt:   17000,
			wantLocal: 17033,
			wantMap:   "17033:17033",
		},
		{
			name:      "an env with no resolved port block falls back to the chart default",
			rangeSt:   0,
			wantLocal: 0,
			wantMap:   "0:17033",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := config.OpenResult{
				Tenant:      "frs",
				Environment: "prod",
				EnvConfig: config.EnvConfig{
					Name:                "prod",
					LocalPortRangeStart: tc.rangeSt,
					KubernetesContext:   "erun",
				},
			}
			if got := config.APIPortForResult(result); got != tc.wantLocal {
				t.Fatalf("expected local port %d, got %d", tc.wantLocal, got)
			}
			if got := apiPortMapping(kubectlAPIPortForwardArgs(result, tc.wantLocal)); got != tc.wantMap {
				t.Fatalf("expected the port mapping %q, got %q", tc.wantMap, got)
			}
		})
	}
}

var portMappingPattern = regexp.MustCompile(`^\d+:\d+$`)

func apiPortMapping(args []string) string {
	for _, arg := range args {
		if portMappingPattern.MatchString(arg) {
			return arg
		}
	}
	return ""
}
