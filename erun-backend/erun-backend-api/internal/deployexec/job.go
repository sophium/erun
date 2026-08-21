// Package deployexec runs a hosted environment's deploy server-side. The backend
// itself has no helm/kubectl, so instead of embedding the deploy tooling it
// creates a Kubernetes Job that runs `erun deploy` inside the erun-devops
// runtime image — which already carries erun + helm + kubectl — under a
// cluster-admin ServiceAccount, and watches that Job to completion. The Job
// launch, watch and failure read-back are the shared jobexec machinery; this
// package owns only the deploy's own Job shape. The durable DBOS workflow that
// moves the environment's status around it lives in the provision package and
// drives this launcher through the service coordinator.
package deployexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
)

const (
	// A deploy that runs longer than this is treated as wedged.
	jobActiveDeadlineSeconds int64 = 30 * 60
	// No in-Job retries: the provisioning workflow owns re-runs, so a failed
	// deploy surfaces as failed rather than silently retrying under a stale spec.
	jobBackoffLimit int32 = 0
	// Jobs are cleaned up by the workflow after it reads the outcome; keep them
	// briefly for status/logs.
	jobTTLSecondsAfterFinished int32 = 10 * 60
)

// Outcome and Result are the shared terminal shapes; deploy callers keep naming
// them through this package.
type Outcome = jobexec.Outcome

const (
	OutcomeSucceeded = jobexec.OutcomeSucceeded
	OutcomeFailed    = jobexec.OutcomeFailed
)

type Result = jobexec.Result

// deployContainerName is the Job's single container, named so the log read can
// address it explicitly.
const deployContainerName = "deploy"

// DeployJobParams is the input to one hosted-env deploy Job.
type DeployJobParams struct {
	Tenant      string
	Environment string
	Version     string
	// DeployID scopes the Job to one deploy attempt. It is carried in the durable
	// workflow input, so a resumed workflow rebuilds the same Job name and
	// re-watches its in-flight Job, while a fresh attempt gets its own Job rather
	// than re-reading the previous attempt's terminal outcome. Empty keeps the
	// version-scoped name the create path has always used.
	DeployID string
	// Namespace the Job runs in (the platform namespace where the backend lives).
	Namespace string
	// Image is the erun-devops runtime image carrying erun + helm + kubectl.
	Image string
	// ServiceAccount is a cluster-admin SA so the in-Job `erun deploy` can create
	// the target namespace and install the runtime chart.
	ServiceAccount string
	// ExposeTargetIP is the platform's ingress IP the per-env wildcard DNS record
	// points at. Empty (the default, unset ERUN_ENV_EXPOSE_TARGET_IP) leaves the
	// Job at the plain `erun deploy` it has always run — no attempt to expose,
	// no new failure mode. Set, the Job chains `erun expose <tenant> <environment>
	// mcp --ip <ExposeTargetIP> --skip-if-unconfigured` after a successful
	// deploy: --skip-if-unconfigured makes the chain a no-op success for any
	// tenant whose own project declares no platform block, so this only ever
	// wires DNS+Ingress for an actual erunpaas platform deployment.
	ExposeTargetIP string
	// ExposeServicesZone/ExposePlatformNamespace thread expose's --services-zone/
	// --platform-namespace: the deploy Job has no git checkout, so `erun expose`
	// cannot resolve these from a project's .erun/config.yaml the way an
	// interactive run does (#1086 — project resolution fails outright with
	// "cannot find git project", which --skip-if-unconfigured cannot cover
	// because the skip decision itself needs a resolved project). The control
	// plane already carries both for its own purposes — ExposeServicesZone from
	// the same services zone its DNS-01 cert issuance uses, ExposePlatformNamespace
	// from the namespace its own Jobs (and, in a self-hosted platform, the
	// PowerDNS singleton) run in — so this reuses that knowledge rather than
	// inventing a second source for it. Either left empty falls the chained
	// expose back to project-based resolution, which fails exactly as it always
	// has for a sourceless Job; since exposure is best-effort (see
	// exposeChainScript), that failure no longer takes the deploy down with it.
	ExposeServicesZone      string
	ExposePlatformNamespace string
	// RuntimeImageOverride, when set, threads `--runtime-image <value>` to the
	// in-Job `erun deploy` — the caller's bootstrap decision for a tenant with no
	// published <tenant>-devops image: install the canonical published
	// erun-devops chart+image by reference instead of resolving artifacts that do
	// not exist. Empty leaves the Job's deploy command exactly as it was before
	// this existed, deploying the tenant's own artifacts.
	RuntimeImageOverride string
	// MaxCPU/MaxMemory/MaxStorage are Kubernetes quantity strings (e.g. "4",
	// "8Gi", "80Gi") threaded to `erun deploy` as --max-cpu/--max-memory/
	// --max-storage, capping the environment's namespace with a ResourceQuota +
	// LimitRange (erun-common/kubernetes_resource_quota.go). Empty leaves the
	// Job's deploy command unchanged from before these existed. All three are
	// set together or not at all (routes.validateNamespaceQuotaFloor and
	// eruncommon.ValidateNamespaceResourceQuota both require completeness).
	MaxCPU     string
	MaxMemory  string
	MaxStorage string
	// MaxCPUMillicores/MaxMemoryMB/MaxStorageGB are the same cap as
	// MaxCPU/MaxMemory/MaxStorage in their original numeric form, carried
	// alongside the Kubernetes quantity strings so a usage-metering caller
	// (service.EnvironmentProvisioner) can record the applied cap without
	// re-parsing a quantity string.
	MaxCPUMillicores int
	MaxMemoryMB      int
	MaxStorageGB     int
}

// mcpExposeService is the logical service name the deploy Job exposes: the
// env's MCP edge, reachable at mcp.<tenant>-<environment>.<services zone> —
// the hostname #605's acceptance criterion names.
const mcpExposeService = "mcp"

// buildDeployJob renders the deploy Job. The container runs a non-interactive
// `erun deploy <tenant> <env> --version <version>`; in-cluster, erun resolves
// the kube context from the mounted ServiceAccount.
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
					}},
				},
			},
		},
	}
}

// buildDeployCommand composes the Job's argv. The Job's `command` replaces the
// image's entrypoint, so none of the entrypoint's usual setup runs: no
// in-cluster kubeconfig (entrypoint.sh's write_kubeconfig) and no
// ~/.config/erun/<tenant>/<environment>/config.yaml for `erun deploy` to
// resolve — a freshly-registered environment was never baked into any image,
// so nothing on disk describes it. bootstrapEnvironmentScript seeds both
// explicitly before `erun deploy` runs, keeping `deploy` itself an unchanged
// pure primitive: the Job (this caller) supplies the environment's shape, the
// primitive still only consumes on-disk config exactly as it always has.
func buildDeployCommand(params DeployJobParams) []string {
	deploy := []string{"erun", "deploy", params.Tenant, params.Environment, "--version", params.Version}
	if override := strings.TrimSpace(params.RuntimeImageOverride); override != "" {
		deploy = append(deploy, "--runtime-image", override)
	}
	deploy = append(deploy, namespaceQuotaFlags(params)...)
	script := bootstrapEnvironmentScript(params.Tenant, params.Environment) + shellJoin(deploy)
	if ip := strings.TrimSpace(params.ExposeTargetIP); ip != "" {
		expose := []string{"erun", "expose", params.Tenant, params.Environment, mcpExposeService, "--ip", ip, "--skip-if-unconfigured"}
		if zone, ns := strings.TrimSpace(params.ExposeServicesZone), strings.TrimSpace(params.ExposePlatformNamespace); zone != "" && ns != "" {
			expose = append(expose, "--services-zone", zone, "--platform-namespace", ns)
		}
		script += exposeChainScript(expose)
	}
	return []string{"sh", "-c", script}
}

// exposeFailureMarker prefixes the line exposeChainScript prints when the
// chained expose step does not succeed. ExposeFailureFromOutput reads it back
// out of the Job's captured output.
const exposeFailureMarker = "ERUN_EXPOSE_FAILED"

// exposeChainScript runs expose after a successful deploy without letting its
// failure fail the Job (#1086). Exposure (DNS + Ingress) is best-effort: the
// deploy already landed a healthy workload, so failing the whole Job over a
// DNS/Ingress problem would record a running environment as a failed
// provision, exactly the misdirection the issue reported. `{ ... || printf
// ...; }` always exits 0, so the deploy's own `&&` is the only thing that can
// still fail the script; on a real expose failure the marker line carries
// expose's own combined output so ExposeFailureFromOutput can recover it from
// the Job's log after a successful run, instead of the environment silently
// losing the reason it is not exposed.
func exposeChainScript(expose []string) string {
	return " && { expose_out=$(" + shellJoin(expose) + " 2>&1) || printf '" + exposeFailureMarker + ": %s\\n' \"$expose_out\"; }"
}

// ExposeFailureFromOutput extracts the chained expose step's failure detail
// from the deploy Job's captured stdout (jobexec.Options.CaptureOutput), or ""
// when exposure succeeded, was never attempted (ExposeTargetIP unset), or the
// Job predates chaining an expose at all.
func ExposeFailureFromOutput(output string) string {
	marker := exposeFailureMarker + ": "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(output[idx+len(marker):])
}

// namespaceQuotaFlags appends --max-cpu/--max-memory/--max-storage when all
// three are configured. Deliberately all-or-nothing: a partial set would leave
// erun deploy validating an incomplete NamespaceResourceQuota.
func namespaceQuotaFlags(params DeployJobParams) []string {
	cpu, memory, storage := strings.TrimSpace(params.MaxCPU), strings.TrimSpace(params.MaxMemory), strings.TrimSpace(params.MaxStorage)
	if cpu == "" || memory == "" || storage == "" {
		return nil
	}
	return []string{"--max-cpu", cpu, "--max-memory", memory, "--max-storage", storage}
}

// bootstrapEnvironmentScript seeds the in-cluster kubeconfig context
// (mirroring entrypoint.sh's write_kubeconfig, from the ServiceAccount token
// Kubernetes auto-mounts into every pod) and a minimal tenant/env config for
// erun's CLI verbs to resolve, then chains the real command with `&&`. Every
// lifecycle Job — deploy, stop, delete — sets `command`, which replaces the
// image's entrypoint, so none of the entrypoint's usual setup runs; this is
// the one seeding path all three share, so a Job that skips it is a caller
// bug, not a second copy that can drift out of sync. It deliberately does not
// carry a runtime version: nothing this seeds it for reads it back (deploy
// gets its version from the explicit `--version` this seeds a command line
// for, never from the config it writes), so a version here would just be a
// second, unused place to keep in sync. Tenant and environment are
// DNS-1123-label-shaped values already validated upstream
// (routes.decodeCreateEnvironmentInput et al.), so they are safe to
// interpolate directly into the generated YAML.
func bootstrapEnvironmentScript(tenant, environment string) string {
	return fmt.Sprintf(`set -e
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
mkdir -p "$HOME/.config/erun/%[1]s/%[2]s"
cat > "$HOME/.config/erun/%[1]s/config.yaml" <<TENANT_EOF
name: %[1]s
defaultenvironment: %[2]s
TENANT_EOF
cat > "$HOME/.config/erun/%[1]s/%[2]s/config.yaml" <<ENV_EOF
name: %[2]s
type: runtime
kubernetescontext: in-cluster
ENV_EOF
`, tenant, environment)
}

// shellJoin renders argv as a POSIX shell command line, single-quoting every
// argument so tenant/environment/version/IP values (already validated as
// DNS-1123 labels or IPs upstream, but not this function's job to trust) can
// never be interpreted as shell syntax.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// DeployJobName is deterministic in its inputs, so a resumed workflow watches
// the Job it already created (a create conflict is treated as "watch the
// existing one") rather than starting a second rollout. An explicit deploy
// passes a per-attempt deployID so a retry of the same version is a new Job
// instead of a re-read of the previous attempt's outcome.
func DeployJobName(tenant, environment, version, deployID string) string {
	name := jobexec.SanitizeName(tenant + "-" + environment + "-" + version)
	suffix := ""
	if deployID != "" {
		suffix = "-" + jobexec.ShortID(deployID)
	}
	// Kubernetes caps an object name at 63 characters; trim the descriptive
	// middle rather than the attempt suffix, which is what keeps attempts apart.
	if budget := jobexec.MaxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

const jobNamePrefix = "erun-deploy-"

// Launcher creates and watches deploy, stop, and delete Jobs. One instance
// covers all three lifecycle actions; each gets its own jobexec.Runner only
// because Kind/Container differ, not a second launcher.
type Launcher struct {
	runner       *jobexec.Runner
	stopRunner   *jobexec.Runner
	deleteRunner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{
		// CaptureOutput: a successful deploy Job's own log is where
		// ExposeFailureFromOutput finds the chained expose step's marker, since a
		// best-effort expose failure never turns the Job itself into a failure.
		runner:       jobexec.NewRunner(kube, jobexec.Options{Kind: "deploy", Container: deployContainerName, CaptureOutput: true}),
		stopRunner:   jobexec.NewRunner(kube, jobexec.Options{Kind: "stop", Container: stopContainerName}),
		deleteRunner: jobexec.NewRunner(kube, jobexec.Options{Kind: "delete", Container: deleteContainerName}),
	}
}

// PollEvery sets how often every runner's watch re-reads its Job's status.
func (l *Launcher) PollEvery(every time.Duration) {
	l.runner.PollEvery = every
	l.stopRunner.PollEvery = every
	l.deleteRunner.PollEvery = every
}

// Run creates the deploy Job and blocks until it reaches a terminal state,
// returning the result.
func (l *Launcher) Run(ctx context.Context, params DeployJobParams) (Result, error) {
	return l.runner.Run(ctx, buildDeployJob(params))
}
