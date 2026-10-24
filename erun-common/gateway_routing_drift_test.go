package eruncommon

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

const testRuntimeDeploymentWithGateway = `{"spec":{"template":{"spec":{"containers":[
  {"name":"erun-devops","env":[{"name":"CLAUDE_CODE_MAX_OUTPUT_TOKENS","value":"32000"},{"name":"ANTHROPIC_BASE_URL","value":"https://openrouter.ai/api"}]},
  {"name":"erun-dind","env":[]}]}}}}`

const testRuntimeDeploymentWithoutGateway = `{"spec":{"template":{"spec":{"containers":[
  {"name":"erun-devops","env":[{"name":"CLAUDE_CODE_MAX_OUTPUT_TOKENS","value":"32000"}]}]}}}}`

func stubRuntimeDeployment(t *testing.T, raw string, found bool) *[]string {
	t.Helper()
	var calls []string
	previous := loadRuntimeDeploymentJSON
	loadRuntimeDeploymentJSON = func(_ context.Context, kubernetesContext, namespace, deployment string) ([]byte, bool, error) {
		calls = append(calls, kubernetesContext+" "+namespace+" "+deployment)
		return []byte(raw), found, nil
	}
	t.Cleanup(func() { loadRuntimeDeploymentJSON = previous })
	return &calls
}

// TestReadGatewayRoutingReportsAPodStillOnASwitchedOffGateway is the operator's
// report: the gateway was switched off in ERun settings (the catalog keeps its
// models but has no base URL), and the running pod still routes to the gateway.
// Before this existed nothing could say so — the switch looked like it did
// nothing, because only a redeploy applies it.
func TestReadGatewayRoutingReportsAPodStillOnASwitchedOffGateway(t *testing.T) {
	calls := stubRuntimeDeployment(t, testRuntimeDeploymentWithGateway, true)
	switchedOff := &config.OpenRouterConfig{
		DefaultModel: "deepseek/deepseek-v4.1-flash",
		Models:       []config.OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash"}},
	}
	env := config.EnvConfig{Name: "build", KubernetesContext: "erun-build"}

	routing, found, err := ReadGatewayRouting(context.Background(), env, "erun", switchedOff)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if routing.Deployed != "https://openrouter.ai/api" || routing.Wanted != "" {
		t.Fatalf("routing = %+v", routing)
	}
	if !routing.Drifted() {
		t.Fatal("a pod still on a switched-off gateway must read as needing a redeploy")
	}
	if got := strings.Join(*calls, "|"); got != "erun-build erun-build erun-devops" {
		t.Fatalf("read %q, want the runtime deployment in the env's namespace", got)
	}
}

func TestReadGatewayRoutingReportsAnOptedOutEnvironmentStillOnTheGateway(t *testing.T) {
	stubRuntimeDeployment(t, testRuntimeDeploymentWithGateway, true)
	no := false
	env := config.EnvConfig{Name: "ux", KubernetesContext: "erun", Claude: config.EnvironmentClaudeConfig{UseGateway: &no}}

	routing, _, err := ReadGatewayRouting(context.Background(), env, "erun", &config.OpenRouterConfig{BaseURL: testGatewayURL})
	if err != nil {
		t.Fatal(err)
	}
	if !routing.Drifted() {
		t.Fatalf("routing = %+v, want drift for an environment that opted out", routing)
	}
}

func TestReadGatewayRoutingReportsAPodNotYetOnASwitchedOnGateway(t *testing.T) {
	stubRuntimeDeployment(t, testRuntimeDeploymentWithoutGateway, true)
	env := config.EnvConfig{Name: "build", KubernetesContext: "erun-build"}

	routing, _, err := ReadGatewayRouting(context.Background(), env, "erun", &config.OpenRouterConfig{BaseURL: testGatewayURL})
	if err != nil {
		t.Fatal(err)
	}
	if routing.Deployed != "" || routing.Wanted != testGatewayURL || !routing.Drifted() {
		t.Fatalf("routing = %+v, want drift towards the gateway", routing)
	}
}

func TestReadGatewayRoutingAgreesWhenThePodMatches(t *testing.T) {
	stubRuntimeDeployment(t, testRuntimeDeploymentWithGateway, true)
	env := config.EnvConfig{Name: "build", KubernetesContext: "erun-build"}

	// A trailing slash is the same endpoint, not a reason to redeploy.
	routing, _, err := ReadGatewayRouting(context.Background(), env, "erun", &config.OpenRouterConfig{BaseURL: testGatewayURL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if routing.Drifted() {
		t.Fatalf("routing = %+v, want no drift", routing)
	}
}

func TestReadGatewayRoutingWithoutADeployedRuntime(t *testing.T) {
	stubRuntimeDeployment(t, "", false)
	_, found, err := ReadGatewayRouting(context.Background(), config.EnvConfig{Name: "build"}, "erun", &config.OpenRouterConfig{BaseURL: testGatewayURL})
	if err != nil || found {
		t.Fatalf("found=%v err=%v, want not found and no error", found, err)
	}
}
