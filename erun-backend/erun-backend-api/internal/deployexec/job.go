// Package deployexec runs a hosted environment's deploy as a Kubernetes Job
// executing `erun deploy` in the erun-devops runtime image.
package deployexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	jobActiveDeadlineSeconds int64 = 30 * 60
	// No in-Job retries: the provisioning workflow owns re-runs.
	jobBackoffLimit            int32 = 0
	jobTTLSecondsAfterFinished int32 = 10 * 60
)

type Outcome = jobexec.Outcome

const (
	OutcomeSucceeded = jobexec.OutcomeSucceeded
	OutcomeFailed    = jobexec.OutcomeFailed
)

type Result = jobexec.Result

const deployContainerName = "deploy"

// DeployJobParams is the input to one hosted-env deploy Job.
type DeployJobParams struct {
	Tenant      string
	Environment string
	Version     string
	// Placement's zero value targets the Job's own cluster.
	Placement PlacementParams
	// DeployID makes a fresh attempt a new Job while a resumed workflow re-watches
	// the same one; empty keeps the version-scoped name.
	DeployID  string
	Namespace string
	Image     string
	// ServiceAccount must be cluster-admin to create the namespace and install the chart.
	ServiceAccount string
	// ExposeTargetIP, when set, chains `erun expose ... mcp --skip-if-unconfigured`
	// after a successful deploy.
	ExposeTargetIP string
	// The Job has no git checkout, so `erun expose` cannot resolve these from a
	// project; empty falls back to project resolution, which fails best-effort.
	ExposeServicesZone      string
	ExposePlatformNamespace string
	// RuntimeImageOverride installs the canonical erun-devops image for a tenant with
	// no published <tenant>-devops image.
	RuntimeImageOverride string
	// Kubernetes quantity strings for the namespace ResourceQuota; all set or none.
	MaxCPU     string
	MaxMemory  string
	MaxStorage string
	// The same cap in numeric form, for usage metering without re-parsing.
	MaxCPUMillicores int
	MaxMemoryMB      int
	MaxStorageGB     int
	// MCPAuthPublicKeyPEM lets the runtime's MCP edge trust backend-minted tokens;
	// empty keeps the edge loopback-only.
	MCPAuthPublicKeyPEM string
	// Per-env TLS through the DNS-01 broker; only effective with ExposeTargetIP.
	TLSDNS01Token            string
	TLSDNS01BrokerURL        string
	TLSDNS01WebhookGroupName string
	TLSACMEEmail             string
	TLSACMEServer            string
}

// mcpExposeService is reachable at mcp.<tenant>-<environment>.<services zone>.
const mcpExposeService = "mcp"

func buildDeployJob(params DeployJobParams) *batchv1.Job {
	backoff := jobBackoffLimit
	deadline := jobActiveDeadlineSeconds
	ttl := jobTTLSecondsAfterFinished
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeployJobName(params.Tenant, params.Environment, params.Version, params.DeployID),
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "erun-deploy-executor",
				"erun.io/tenant":               params.Tenant,
				"erun.io/environment":          params.Environment,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: params.ServiceAccount,
					Containers: []corev1.Container{{
						Name:    deployContainerName,
						Image:   params.Image,
						Command: buildDeployCommand(params),
						Env:     placementEnvVars(params.Placement),
					}},
				},
			},
		},
	}
}

// `command` replaces the image entrypoint, so bootstrapEnvironmentScript seeds
// the kubeconfig and env config that entrypoint would have written.
func buildDeployCommand(params DeployJobParams) []string {
	deploy := []string{"erun", "deploy", params.Tenant, params.Environment, "--version", params.Version}
	if override := strings.TrimSpace(params.RuntimeImageOverride); override != "" {
		deploy = append(deploy, "--runtime-image", override)
	}
	deploy = append(deploy, namespaceQuotaFlags(params)...)
	script := bootstrapEnvironmentScript(params.Tenant, params.Environment, params.Placement)
	if pem := strings.TrimSpace(params.MCPAuthPublicKeyPEM); pem != "" {
		script += writeMCPAuthPublicKeyScript(pem)
		deploy = append(deploy, "--mcp-auth-public-key", mcpAuthPublicKeyJobPath)
	}
	// Chained-step files must be written before the deploy command: anything appended
	// after shellJoin(deploy) glues onto its last argument.
	exposeFiles, exposeChain := buildExposeChain(params)
	script += exposeFiles
	script += shellJoin(deploy)
	script += exposeChain
	return []string{"sh", "-c", script}
}

const dns01TokenJobPath = "/tmp/erun-deploy-dns01-token"

// Heredoc, never argv, so the token stays out of the command line and process list.
func writeDNS01TokenScript(token string) string {
	return fmt.Sprintf("cat > %s <<'DNS01_TOKEN_EOF'\n%s\nDNS01_TOKEN_EOF\n", dns01TokenJobPath, strings.TrimRight(token, "\n"))
}

func buildExposeChain(params DeployJobParams) (exposeFiles, chain string) {
	ip := strings.TrimSpace(params.ExposeTargetIP)
	if ip == "" {
		return "", ""
	}
	expose := []string{"erun", "expose", params.Tenant, params.Environment, mcpExposeService, "--ip", ip, "--skip-if-unconfigured"}
	if zone, ns := strings.TrimSpace(params.ExposeServicesZone), strings.TrimSpace(params.ExposePlatformNamespace); zone != "" && ns != "" {
		expose = append(expose, "--services-zone", zone, "--platform-namespace", ns)
	}
	tlsScript, tlsFlags := tlsExposeFlags(params)
	expose = append(expose, tlsFlags...)
	return tlsScript, exposeChainScript(expose)
}

func tlsExposeFlags(params DeployJobParams) (script string, flags []string) {
	token, broker, email := strings.TrimSpace(params.TLSDNS01Token), strings.TrimSpace(params.TLSDNS01BrokerURL), strings.TrimSpace(params.TLSACMEEmail)
	if token == "" || broker == "" || email == "" {
		return "", nil
	}
	flags = []string{"--dns01-token-file", dns01TokenJobPath, "--dns01-broker-url", broker, "--acme-email", email}
	if server := strings.TrimSpace(params.TLSACMEServer); server != "" {
		flags = append(flags, "--acme-server", server)
	}
	if group := strings.TrimSpace(params.TLSDNS01WebhookGroupName); group != "" {
		flags = append(flags, "--dns01-webhook-group-name", group)
	}
	return writeDNS01TokenScript(token), flags
}

const exposeFailureMarker = "ERUN_EXPOSE_FAILED"

// Exposure is best-effort: `{ ... || printf ...; }` always exits 0, and the
// marker line preserves expose's output for ExposeFailureFromOutput.
func exposeChainScript(expose []string) string {
	return " && { expose_out=$(" + shellJoin(expose) + " 2>&1) || printf '" + exposeFailureMarker + ": %s\\n' \"$expose_out\"; }"
}

// ExposeFailureFromOutput extracts the chained expose failure, or "" if none.
func ExposeFailureFromOutput(output string) string {
	marker := exposeFailureMarker + ": "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(output[idx+len(marker):])
}

// ExposeHostnameFromParams computes the MCP edge hostname expose produces, or ""
// when the zone or namespace is missing. Callers must also check ExposeFailureFromOutput.
func ExposeHostnameFromParams(params DeployJobParams) string {
	ip := strings.TrimSpace(params.ExposeTargetIP)
	zone := strings.TrimSpace(params.ExposeServicesZone)
	namespace := strings.TrimSpace(params.ExposePlatformNamespace)
	if ip == "" || zone == "" || namespace == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.%s", mcpExposeService, runtimetarget.KubernetesNamespaceName(params.Tenant, params.Environment), zone)
}

func namespaceQuotaFlags(params DeployJobParams) []string {
	cpu, memory, storage := strings.TrimSpace(params.MaxCPU), strings.TrimSpace(params.MaxMemory), strings.TrimSpace(params.MaxStorage)
	if cpu == "" || memory == "" || storage == "" {
		return nil
	}
	return []string{"--max-cpu", cpu, "--max-memory", memory, "--max-storage", storage}
}

// Fixed path outside $HOME so it avoids shellJoin's quoting and works regardless of HOME.
const mcpAuthPublicKeyJobPath = "/tmp/erun-deploy-mcp-auth-public-key.pem"

// Heredoc, never argv, so the key stays out of the command line and process list.
func writeMCPAuthPublicKeyScript(pem string) string {
	return fmt.Sprintf("cat > %s <<'MCP_AUTH_PUBLIC_KEY_EOF'\n%s\nMCP_AUTH_PUBLIC_KEY_EOF\n", mcpAuthPublicKeyJobPath, strings.TrimRight(pem, "\n"))
}

// bootstrapEnvironmentScript seeds the kubeconfig and a minimal tenant/env config
// that every lifecycle Job needs, since `command` bypasses the entrypoint.
// Tenant and environment are validated DNS-1123 labels, safe to interpolate.
func bootstrapEnvironmentScript(tenant, environment string, placement PlacementParams) string {
	return kubeconfigScript(placement) + fmt.Sprintf(`mkdir -p "$HOME/.config/erun/%[1]s/%[2]s"
cat > "$HOME/.config/erun/%[1]s/config.yaml" <<TENANT_EOF
name: %[1]s
defaultenvironment: %[2]s
TENANT_EOF
cat > "$HOME/.config/erun/%[1]s/%[2]s/config.yaml" <<ENV_EOF
name: %[2]s
type: runtime
kubernetescontext: %[3]s
ENV_EOF
`, tenant, environment, placement.kubernetesContextOrInCluster())
}

const inClusterKubeconfigScript = `set -e
mkdir -p "$HOME/.kube"
erun_deploy_ns="$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)"
cat > "$HOME/.kube/config" <<KUBECONFIG_EOF
apiVersion: v1
kind: Config
clusters:
  - cluster:
      certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      server: https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS:-443}
    name: in-cluster
contexts:
  - context:
      cluster: in-cluster
      namespace: ${erun_deploy_ns}
      user: erun-deploy
    name: in-cluster
current-context: in-cluster
users:
  - name: erun-deploy
    user:
      tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
KUBECONFIG_EOF
`

// Remote placement uses kubectl config subcommands so operator-authored strings
// are never shell syntax; the admin token comes from a Secret-populated env var.
func kubeconfigScript(placement PlacementParams) string {
	if !placement.remote() {
		return inClusterKubeconfigScript
	}
	name := placement.KubernetesContext
	lines := []string{
		"set -e",
		shellJoin([]string{"kubectl", "config", "set-cluster", name, "--server", placement.ServerURL, "--insecure-skip-tls-verify=true"}),
		shellJoin([]string{"kubectl", "config", "set-credentials", name, "--token"}) + ` "$` + placementAdminTokenEnvVar + `"`,
		shellJoin([]string{"kubectl", "config", "set-context", name, "--cluster", name, "--user", name}),
		shellJoin([]string{"kubectl", "config", "use-context", name}),
	}
	return strings.Join(lines, "\n") + "\n"
}

// shellJoin single-quotes every argument so no value is ever shell syntax.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// DeployJobName is deterministic so a resumed workflow re-watches its Job; deployID
// separates attempts of the same version.
func DeployJobName(tenant, environment, version, deployID string) string {
	name := jobexec.SanitizeName(tenant + "-" + environment + "-" + version)
	suffix := ""
	if deployID != "" {
		suffix = "-" + jobexec.ShortID(deployID)
	}
	// Trim the middle, not the attempt suffix, to fit the 63-character name limit.
	if budget := jobexec.MaxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

const jobNamePrefix = "erun-deploy-"

// Launcher creates and watches deploy, stop, and delete Jobs.
type Launcher struct {
	kube         kubernetes.Interface
	runner       *jobexec.Runner
	stopRunner   *jobexec.Runner
	deleteRunner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{
		kube: kube,
		// A best-effort expose failure only shows in a successful Job's log.
		runner:       jobexec.NewRunner(kube, jobexec.Options{Kind: "deploy", Container: deployContainerName, CaptureOutput: true}),
		stopRunner:   jobexec.NewRunner(kube, jobexec.Options{Kind: "stop", Container: stopContainerName}),
		deleteRunner: jobexec.NewRunner(kube, jobexec.Options{Kind: "delete", Container: deleteContainerName, CaptureOutput: true}),
	}
}

func (l *Launcher) PollEvery(every time.Duration) {
	l.runner.PollEvery = every
	l.stopRunner.PollEvery = every
	l.deleteRunner.PollEvery = every
}

// Run creates the deploy Job and blocks until it is terminal.
func (l *Launcher) Run(ctx context.Context, params DeployJobParams) (Result, error) {
	if err := ensurePlacementSecret(ctx, l.kube, params.Namespace, params.Placement); err != nil {
		return Result{}, fmt.Errorf("provision placement credential: %w", err)
	}
	return l.runner.Run(ctx, buildDeployJob(params))
}
