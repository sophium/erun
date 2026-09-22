package cmd

import (
	"regexp"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// TestKubectlAPIPortForwardArgsResolvesTheEnvironmentsOwnServicePort is the
// reproduction for the environment whose api-port is not the canonical 17033.
// The API component chart publishes its Service on `{{ default 17033
// .Values.apiPort }}`, which the deploy sets from the environment's own port
// block, so the remote side of the forward has to come from that same block.
// Pinned to common.APIServicePort it asked kubectl for a service port the
// Service does not expose, which broke the API tunnel on every environment
// outside the 17000 block while the environment itself was healthy.
func TestKubectlAPIPortForwardArgsResolvesTheEnvironmentsOwnServicePort(t *testing.T) {
	// The injected ERUN_* ports outrank the config-derived block, so an
	// in-pod test process would otherwise resolve these fixtures to whatever
	// environment it is running in.
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
			name:      "an env outside the 17000 block forwards to its own service port",
			rangeSt:   17300,
			wantLocal: 17333,
			wantMap:   "17333:17333",
		},
		{
			name:      "the canonical-block env still forwards to 17033",
			rangeSt:   17000,
			wantLocal: 17033,
			wantMap:   "17033:17033",
		},
		{
			// An env that resolves no port block at all deploys the chart's
			// `default 17033`, so that is the port to dial -- the same fallback
			// the deploy's own apiPort --set makes.
			name:      "an env with no resolved port block falls back to the chart default",
			rangeSt:   0,
			wantLocal: 0,
			wantMap:   "0:17033",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := common.OpenResult{
				Tenant:      "frs",
				Environment: "prod",
				EnvConfig: common.EnvConfig{
					Name:                "prod",
					LocalPortRangeStart: tc.rangeSt,
					KubernetesContext:   "erun",
				},
			}
			if got := common.APIPortForResult(result); got != tc.wantLocal {
				t.Fatalf("expected local port %d, got %d", tc.wantLocal, got)
			}
			if got := apiPortMapping(kubectlAPIPortForwardArgs(result, tc.wantLocal)); got != tc.wantMap {
				t.Fatalf("expected the port mapping %q, got %q", tc.wantMap, got)
			}
		})
	}
}

var portMappingPattern = regexp.MustCompile(`^\d+:\d+$`)

// apiPortMapping returns the single <local>:<remote> argument of a kubectl
// port-forward argv.
func apiPortMapping(args []string) string {
	for _, arg := range args {
		if portMappingPattern.MatchString(arg) {
			return arg
		}
	}
	return ""
}
