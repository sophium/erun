package deployexec

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testParams() DeployJobParams {
	return DeployJobParams{
		Tenant:         "acme",
		Environment:    "prod",
		Version:        "1.0.149",
		Namespace:      "acme-platform",
		Image:          "ghcr.io/sophium/acme-devops:1.0.149",
		ServiceAccount: "acme-api-deployer",
	}
}

func TestBuildDeployJobSpec(t *testing.T) {
	job := buildDeployJob(testParams())

	if job.Namespace != "acme-platform" {
		t.Fatalf("namespace = %q", job.Namespace)
	}
	if job.Name != "erun-deploy-acme-prod-1-0-149" {
		t.Fatalf("name = %q, want erun-deploy-acme-prod-1-0-149 (dots sanitized)", job.Name)
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != "acme-api-deployer" {
		t.Fatalf("service account = %q", pod.ServiceAccountName)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restart policy = %q, want Never", pod.RestartPolicy)
	}
	if len(pod.Containers) != 1 || pod.Containers[0].Image != "ghcr.io/sophium/acme-devops:1.0.149" {
		t.Fatalf("container image = %+v", pod.Containers)
	}
	assertDeployBootstrapScript(t, pod.Containers[0].Command)
	// No in-Job retries: a failed deploy must surface, not silently retry.
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
}

// assertDeployBootstrapScript checks the Job's command seeds the in-cluster
// kubeconfig and the environment's config before running the real `erun
// deploy` — the Job's command replaces the image's entrypoint, so nothing
// else seeds either.
func assertDeployBootstrapScript(t *testing.T, command []string) {
	t.Helper()
	assertCommand(t, command[:2], []string{"sh", "-c"})
	if len(command) != 3 {
		t.Fatalf("command = %v, want sh -c '<script>'", command)
	}
	script := command[2]
	for _, want := range []string{
		"$HOME/.kube/config",
		"name: in-cluster",
		"$HOME/.config/erun/acme/config.yaml",
		"$HOME/.config/erun/acme/prod/config.yaml",
		"type: runtime",
		"kubernetescontext: in-cluster",
		"'erun' 'deploy' 'acme' 'prod' '--version' '1.0.149'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script %q missing %q", script, want)
		}
	}
}

// TestBuildDeployJobSpecWithNamespaceQuota: all three of MaxCPU/MaxMemory/
// MaxStorage append the --max-cpu/--max-memory/--max-storage flags; a partial
// set (only one configured) appends none, since erun deploy validates the
// three together.
func TestBuildDeployJobSpecWithNamespaceQuota(t *testing.T) {
	params := testParams()
	params.MaxCPU, params.MaxMemory, params.MaxStorage = "4000m", "9216Mi", "80Gi"
	script := buildDeployCommand(params)[2]
	want := "'erun' 'deploy' 'acme' 'prod' '--version' '1.0.149' '--max-cpu' '4000m' '--max-memory' '9216Mi' '--max-storage' '80Gi'"
	if !strings.Contains(script, want) {
		t.Fatalf("script %q missing %q", script, want)
	}

	partial := testParams()
	partial.MaxCPU = "4000m"
	partialScript := buildDeployCommand(partial)[2]
	if strings.Contains(partialScript, "--max-cpu") {
		t.Fatalf("script %q should not apply a partial namespace quota", partialScript)
	}
}

// TestBuildDeployCommandWithRuntimeImageOverride: a bootstrap deploy
// threads --runtime-image at the in-Job `erun deploy` so the installed runtime
// chart matches the canonical image the Job's own container runs, rather than
// resolving the tenant's own (never-published) artifacts. Unset leaves the
// command exactly as it was before this existed.
func TestBuildDeployCommandWithRuntimeImageOverride(t *testing.T) {
	params := testParams()
	params.RuntimeImageOverride = "ghcr.io/sophium/erun-devops:1.0.149"
	script := buildDeployCommand(params)[2]
	want := "'erun' 'deploy' 'acme' 'prod' '--version' '1.0.149' '--runtime-image' 'ghcr.io/sophium/erun-devops:1.0.149'"
	if !strings.Contains(script, want) {
		t.Fatalf("script %q missing %q", script, want)
	}

	unset := buildDeployCommand(testParams())[2]
	if strings.Contains(unset, "--runtime-image") {
		t.Fatalf("script %q should carry no --runtime-image when unset", unset)
	}
}

// TestBuildDeployJobSpecWithExpose: a configured ExposeTargetIP chains a
// second, independently-skippable `erun expose` after the deploy rather than
// teaching deploy itself about exposure — the Job is the caller composing
// pure primitives.
func TestBuildDeployJobSpecWithExpose(t *testing.T) {
	params := testParams()
	params.ExposeTargetIP = "203.0.113.10"
	job := buildDeployJob(params)
	pod := job.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("containers = %+v", pod.Containers)
	}
	command := pod.Containers[0].Command
	assertCommand(t, command[:2], []string{"sh", "-c"})
	if len(command) != 3 {
		t.Fatalf("command = %v, want sh -c '<script>'", command)
	}
	script := command[2]
	wantDeploy := "'erun' 'deploy' 'acme' 'prod' '--version' '1.0.149'"
	wantExpose := "'erun' 'expose' 'acme' 'prod' 'mcp' '--ip' '203.0.113.10' '--skip-if-unconfigured'"
	if !strings.Contains(script, wantDeploy) {
		t.Fatalf("script %q missing deploy stage %q", script, wantDeploy)
	}
	if !strings.Contains(script, wantExpose) {
		t.Fatalf("script %q missing expose stage %q", script, wantExpose)
	}
	deployIdx := strings.Index(script, wantDeploy)
	exposeIdx := strings.Index(script, wantExpose)
	if deployIdx < 0 || exposeIdx < 0 || deployIdx > exposeIdx {
		t.Fatalf("script %q must run deploy before expose", script)
	}
	if !strings.Contains(script, " && ") {
		t.Fatalf("script %q must short-circuit on a failed deploy", script)
	}
}

// TestBuildDeployJobSpecWithExposePlatformCoordinates: when the control plane
// supplies the services zone and platform namespace it already knows, they
// thread onto the chained `erun expose` as --services-zone/--platform-namespace
// so the sourceless Job can resolve a hostname without a git checkout.
func TestBuildDeployJobSpecWithExposePlatformCoordinates(t *testing.T) {
	params := testParams()
	params.ExposeTargetIP = "203.0.113.10"
	params.ExposeServicesZone = "services.erunpaas.com"
	params.ExposePlatformNamespace = "frs-prod"
	script := buildDeployCommand(params)[2]
	want := "'erun' 'expose' 'acme' 'prod' 'mcp' '--ip' '203.0.113.10' '--skip-if-unconfigured' " +
		"'--services-zone' 'services.erunpaas.com' '--platform-namespace' 'frs-prod'"
	if !strings.Contains(script, want) {
		t.Fatalf("script %q missing %q", script, want)
	}

	// Half the pair configured is the same as neither: expose falls back to its
	// own project-based resolution rather than running with an incomplete
	// override.
	partial := testParams()
	partial.ExposeTargetIP = "203.0.113.10"
	partial.ExposeServicesZone = "services.erunpaas.com"
	partialScript := buildDeployCommand(partial)[2]
	if strings.Contains(partialScript, "--services-zone") {
		t.Fatalf("script %q should not apply a partial platform-coordinates override", partialScript)
	}
}

// TestExposeChainScriptIsBestEffort: the chained expose step must never fail
// the Job it rides on — a healthy deploy must not be recorded as a
// failed provision just because DNS/Ingress wiring failed. On failure it
// prints a marker line ExposeFailureFromOutput reads back out of the Job's
// captured output.
func TestExposeChainScriptIsBestEffort(t *testing.T) {
	params := testParams()
	params.ExposeTargetIP = "203.0.113.10"
	script := buildDeployCommand(params)[2]
	if !strings.Contains(script, "|| printf '"+exposeFailureMarker+": %s\\n' \"$expose_out\"") {
		t.Fatalf("script %q must swallow a failing expose behind the marker", script)
	}
	if strings.HasSuffix(strings.TrimSpace(script), "&& "+shellJoin([]string{"erun", "expose"})) {
		t.Fatalf("script %q must not let expose's own exit status end the script", script)
	}
}

// TestExposeFailureFromOutput: the marker line is how a *successful* Job
// (deploy landed, expose did not) hands back the reason, since the Job's own
// outcome no longer reflects an expose failure.
func TestExposeFailureFromOutput(t *testing.T) {
	output := "==> Deployed acme/prod 1.2.3 in 4s\n" +
		"audit: erun expose --ip 203.0.113.10 --skip-if-unconfigured acme prod mcp\n" +
		exposeFailureMarker + ": cannot find git project\n"
	if got := ExposeFailureFromOutput(output); got != "cannot find git project" {
		t.Fatalf("ExposeFailureFromOutput = %q, want %q", got, "cannot find git project")
	}
	if got := ExposeFailureFromOutput("==> Deployed acme/prod 1.2.3 in 4s\n"); got != "" {
		t.Fatalf("ExposeFailureFromOutput = %q, want empty when no marker is present", got)
	}
}

// TestExposeHostnameFromParams: the reported hostname must match exactly what
// buildDeployCommand actually threads onto the chained `erun expose` call —
// same tenant/environment namespace formula, same services zone — and stay
// empty whenever the chain would not have run with resolved platform
// coordinates (mirroring TestBuildDeployJobSpecWithExposePlatformCoordinates's
// "half the pair configured is the same as neither" case).
func TestExposeHostnameFromParams(t *testing.T) {
	params := testParams()
	params.ExposeTargetIP = "203.0.113.10"
	params.ExposeServicesZone = "services.erunpaas.com"
	params.ExposePlatformNamespace = "frs-prod"
	if got, want := ExposeHostnameFromParams(params), "mcp.acme-prod.services.erunpaas.com"; got != want {
		t.Fatalf("ExposeHostnameFromParams = %q, want %q", got, want)
	}

	noIP := testParams()
	noIP.ExposeServicesZone = "services.erunpaas.com"
	noIP.ExposePlatformNamespace = "frs-prod"
	if got := ExposeHostnameFromParams(noIP); got != "" {
		t.Fatalf("ExposeHostnameFromParams = %q, want empty with no ExposeTargetIP", got)
	}

	partial := testParams()
	partial.ExposeTargetIP = "203.0.113.10"
	partial.ExposeServicesZone = "services.erunpaas.com"
	if got := ExposeHostnameFromParams(partial); got != "" {
		t.Fatalf("ExposeHostnameFromParams = %q, want empty with only a partial platform-coordinates override", got)
	}
}

// TestBuildDeployCommandWithMCPAuthPublicKey: the backend's own MCP-signing
// public key is written to a fixed path via heredoc (never argv) and threaded
// to `erun deploy` as --mcp-auth-public-key, so the runtime's MCP edge trusts
// tokens the backend mints for the console.
func TestBuildDeployCommandWithMCPAuthPublicKey(t *testing.T) {
	params := testParams()
	params.MCPAuthPublicKeyPEM = "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"
	script := buildDeployCommand(params)[2]
	if !strings.Contains(script, "cat > "+mcpAuthPublicKeyJobPath+" <<'MCP_AUTH_PUBLIC_KEY_EOF'") {
		t.Fatalf("script %q must write the public key via heredoc", script)
	}
	if !strings.Contains(script, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("script %q must carry the key content", script)
	}
	want := "'--mcp-auth-public-key' '" + mcpAuthPublicKeyJobPath + "'"
	if !strings.Contains(script, want) {
		t.Fatalf("script %q missing %q", script, want)
	}
	// Empty stays exactly the plain deploy — no heredoc, no flag.
	plain := buildDeployCommand(testParams())[2]
	if strings.Contains(plain, "mcp-auth-public-key") {
		t.Fatalf("script %q should not thread an empty key", plain)
	}
}

// TestBuildDeployCommandWithTLSCertProvisioning: when the control plane mints
// a per-env DNS-01 broker token and supplies the broker/ACME coordinates, the
// deploy Job writes the token via heredoc and threads it plus the coordinates
// onto the chained `erun expose` so it can provision the env's own TLS
// Issuer+Certificate. Only takes effect alongside ExposeTargetIP.
func TestBuildDeployCommandWithTLSCertProvisioning(t *testing.T) {
	params := testParams()
	params.ExposeTargetIP = "203.0.113.10"
	params.TLSDNS01Token = "test-token"
	params.TLSDNS01BrokerURL = "https://api.acme-platform.services.example.com/v1/dns01"
	params.TLSACMEEmail = "admin@example.com"
	script := buildDeployCommand(params)[2]
	if !strings.Contains(script, "cat > "+dns01TokenJobPath+" <<'DNS01_TOKEN_EOF'") {
		t.Fatalf("script %q must write the dns01 token via heredoc", script)
	}
	if !strings.Contains(script, "test-token") {
		t.Fatalf("script %q must carry the token content", script)
	}
	want := "'--dns01-token-file' '" + dns01TokenJobPath + "' '--dns01-broker-url' 'https://api.acme-platform.services.example.com/v1/dns01' '--acme-email' 'admin@example.com'"
	if !strings.Contains(script, want) {
		t.Fatalf("script %q missing %q", script, want)
	}

	// Without ExposeTargetIP there is no chained expose to thread the TLS
	// flags onto at all.
	noExpose := testParams()
	noExpose.TLSDNS01Token = "test-token"
	noExpose.TLSDNS01BrokerURL = "https://api.example.com/v1/dns01"
	noExpose.TLSACMEEmail = "admin@example.com"
	noExposeScript := buildDeployCommand(noExpose)[2]
	if strings.Contains(noExposeScript, "dns01-token-file") {
		t.Fatalf("script %q should not thread tls flags without ExposeTargetIP", noExposeScript)
	}

	// A partial TLS config is the same as none: expose stays plain.
	partial := testParams()
	partial.ExposeTargetIP = "203.0.113.10"
	partial.TLSDNS01Token = "test-token"
	partialScript := buildDeployCommand(partial)[2]
	if strings.Contains(partialScript, "dns01-token-file") {
		t.Fatalf("script %q should not apply a partial tls config", partialScript)
	}
}

// TestBuildDeployCommandWritesChainedStepFilesBeforeTheDeploy guards the
// script's composition rather than its contents. exposeChainScript opens with
// " && " so exposure stays conditional on the deploy succeeding, which means
// anything appended after shellJoin(deploy) welds itself onto the deploy's last
// quoted argument instead of becoming its own command. That turned
// --mcp-auth-public-key into a path ending "pemcat" and failed every hosted
// provision, while assertions asking only whether the script *contained* each
// piece stayed green -- the corruption is a boundary, not a missing piece.
func TestBuildDeployCommandWritesChainedStepFilesBeforeTheDeploy(t *testing.T) {
	params := testParams()
	params.MCPAuthPublicKeyPEM = "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----"
	params.ExposeTargetIP = "203.0.113.10"
	params.TLSDNS01Token = "test-token"
	params.TLSDNS01BrokerURL = "https://api.example.com/v1/dns01"
	params.TLSACMEEmail = "admin@example.com"
	script := buildDeployCommand(params)[2]

	tokenWrite := strings.Index(script, "cat > "+dns01TokenJobPath)
	deployRun := strings.Index(script, "\x27erun\x27 \x27deploy\x27")
	exposeChain := strings.Index(script, "&& { expose_out=")
	if tokenWrite < 0 || deployRun < 0 || exposeChain < 0 {
		t.Fatalf("script must write the token, run the deploy and chain expose, got %q", script)
	}
	if tokenWrite > deployRun {
		t.Errorf("the token heredoc must run before the deploy, got token at %d, deploy at %d in %q", tokenWrite, deployRun, script)
	}
	if exposeChain < deployRun {
		t.Errorf("expose must chain after the deploy so it stays conditional on it, got %q", script)
	}
	if strings.Contains(script, "\x27cat > ") {
		t.Errorf("a heredoc is welded onto the preceding shell word in %q", script)
	}
	if want := "\x27--mcp-auth-public-key\x27 \x27" + mcpAuthPublicKeyJobPath + "\x27"; !strings.Contains(script, want) {
		t.Errorf("script %q must pass the mcp auth key path as its own shell word", script)
	}
}

// TestBuildDeployCommandQuotesArguments guards the shell-injection surface: an
// argument carrying a single quote must stay a single shell word — the
// canonical way a naively-quoted value breaks out of its own quoting.
func TestBuildDeployCommandQuotesArguments(t *testing.T) {
	params := testParams()
	params.Environment = "prod'; rm -rf /; echo"
	params.ExposeTargetIP = "203.0.113.10"
	command := buildDeployCommand(params)
	script := command[2]
	want := `'prod'\''; rm -rf /; echo'`
	if !strings.Contains(script, want) {
		t.Fatalf("script %q should quote the argument as %q", script, want)
	}
}

// assertCommand compares the deploy argv element by element, so a mismatch names
// the position that differs.
func assertCommand(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDeployJobNameSeparatesAttempts: a re-deploy must be a new Job, otherwise
// it would re-read the previous attempt's terminal outcome instead of running.
func TestDeployJobNameSeparatesAttempts(t *testing.T) {
	first := DeployJobName("acme", "prod", "1.0.149", "3f2a91cc-1111-2222-3333-444455556666")
	second := DeployJobName("acme", "prod", "1.0.149", "9b7d40aa-1111-2222-3333-444455556666")
	if first == second {
		t.Fatalf("two attempts share the job name %q", first)
	}
	// The create path passes no attempt id and keeps its stable name, so a
	// resumed workflow still re-watches the Job it already created.
	if got := DeployJobName("acme", "prod", "1.0.149", ""); got != "erun-deploy-acme-prod-1-0-149" {
		t.Fatalf("name without an attempt id = %q", got)
	}
	if !strings.HasPrefix(first, "erun-deploy-acme-prod-1-0-149-") {
		t.Fatalf("attempt name %q lost its readable prefix", first)
	}
}

// TestDeployJobNameFitsKubernetesLimit: names are trimmed in the descriptive
// middle, never in the attempt suffix that keeps two deploys apart.
func TestDeployJobNameFitsKubernetesLimit(t *testing.T) {
	longEnv := strings.Repeat("e", 63)
	first := DeployJobName("verylongtenantname", longEnv, "1.0.149-rc.20260816", "3f2a91cc-aaaa")
	second := DeployJobName("verylongtenantname", longEnv, "1.0.149-rc.20260816", "9b7d40aa-bbbb")
	for _, name := range []string{first, second} {
		if len(name) > 63 {
			t.Fatalf("job name %q is %d characters, over the 63 Kubernetes allows", name, len(name))
		}
	}
	if first == second {
		t.Fatal("truncation collapsed two attempts onto one job name")
	}
}

// TestRunWatchesTheDeployJob holds the launcher to creating and watching the Job
// its params describe; the watch and failure read-back themselves belong to
// jobexec and are covered there.
func TestRunWatchesTheDeployJob(t *testing.T) {
	p := testParams()
	kube := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeployJobName(p.Tenant, p.Environment, p.Version, p.DeployID),
			Namespace: p.Namespace,
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	})
	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	result, err := launcher.Run(context.Background(), p)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", result.Outcome)
	}
}

// TestRunProvisionsThePlacementSecretBeforeTheJob locks #1112's ordering: a
// deploy targeting a remote context must have its credential Secret in place
// before the Job that reads it via secretKeyRef is created, or the Job's
// container fails to start on a missing Secret. The pre-seeded Job here
// (mirroring TestRunWatchesTheDeployJob) lets the runner's create-tolerant-
// of-AlreadyExists path just watch it; the Job's own container/env wiring is
// covered directly by TestBuildDeployJobSpecWithPlacement, which builds the
// Job object without going through a fake watch.
func TestRunProvisionsThePlacementSecretBeforeTheJob(t *testing.T) {
	p := testParams()
	p.Placement = PlacementParams{ContextID: "ctx-1", KubernetesContext: "prod-cluster", ServerURL: "https://203.0.113.10:6443", AdminToken: "the-token"}
	kube := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeployJobName(p.Tenant, p.Environment, p.Version, p.DeployID),
			Namespace: p.Namespace,
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	})
	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	if _, err := launcher.Run(context.Background(), p); err != nil {
		t.Fatalf("run: %v", err)
	}
	secret, err := kube.CoreV1().Secrets(p.Namespace).Get(context.Background(), PlacementSecretName("ctx-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the placement secret must exist after Run: %v", err)
	}
	if secret.StringData[placementAdminTokenKey] != "the-token" {
		t.Fatalf("secret token = %q, want the-token", secret.StringData[placementAdminTokenKey])
	}
}

// TestBuildDeployJobSpecWithPlacement locks that a remote placement's Job
// carries the env var sourcing its admin token from the placement Secret.
func TestBuildDeployJobSpecWithPlacement(t *testing.T) {
	p := testParams()
	p.Placement = PlacementParams{ContextID: "ctx-1", KubernetesContext: "prod-cluster", ServerURL: "https://203.0.113.10:6443"}
	job := buildDeployJob(p)
	envVars := job.Spec.Template.Spec.Containers[0].Env
	if len(envVars) != 1 || envVars[0].ValueFrom == nil || envVars[0].ValueFrom.SecretKeyRef == nil {
		t.Fatalf("job container env = %+v, want it to source the token from the placement secret", envVars)
	}
	if envVars[0].ValueFrom.SecretKeyRef.Name != PlacementSecretName("ctx-1") {
		t.Fatalf("secret ref = %q, want %q", envVars[0].ValueFrom.SecretKeyRef.Name, PlacementSecretName("ctx-1"))
	}
}
