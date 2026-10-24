package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// resolveHelmDeployChartPath returns the chart path to deploy and an optional
// cleanup the caller must defer. A published OCI chart cannot be copied and
// stamped locally; its version is pinned on the helm command line instead (see
// command()), so it deploys from its original reference with no cleanup. A
// local chart at a pinned version is copied and stamped into a temp dir, whose
// removal is the returned cleanup.
func resolveHelmDeployChartPath(params HelmDeployParams) (string, func(), error) {
	if strings.TrimSpace(params.Version) == "" || isOCIChartReference(params.ChartPath) {
		return params.ChartPath, nil, nil
	}
	chartPath, cleanup, err := prepareHelmChartForDeploy(params.ChartPath, params.Version)
	if err != nil {
		return "", nil, err
	}
	return chartPath, cleanup, nil
}

func helmDeployCommandSpec(params HelmDeployParams, chartPath string) helm.CommandSpec {
	// The embedded spec is the traced spec verbatim; only the resolved chart path
	// differs (a local umbrella unpacks to a temp dir). Building the command from
	// it guarantees the executed helm command matches what RunHelmDeploy traced.
	spec := params.HelmDeploySpec
	spec.ChartPath = chartPath
	return spec.command()
}

// helmOutputCapture holds helm's stdout and stderr on separate buffers. os/exec
// runs one copier goroutine per stream it wires up, and Cmd.stderr() only reuses
// the stdout descriptor (making the two safe to alias) when cmd.Stderr is the
// same interface value as cmd.Stdout via interfaceEqual -- here cmd.Stdout is a
// *bytes.Buffer (or a tee) and cmd.Stderr is an io.MultiWriter, so that check
// never fires and a single combined buffer would be written by both copier
// goroutines at once. combined() is read only from classifyHelmDeployResult,
// which runs after runHelmDeployWithPodWatch has already waited out cmd.Wait()
// -- the point os/exec guarantees both copier goroutines have finished -- so no
// further synchronization is needed there.
type helmOutputCapture struct {
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// combined joins the captured streams for the classify error text.
func (c *helmOutputCapture) combined() string {
	return c.stdout.String() + c.stderr.String()
}

// configureHelmDeployCmdOutput wires the command's stdout/stderr and returns
// the capture used for error classification. At VerbosityInfo helm output is
// captured silently so a successful run is quiet; the capture feeds back into
// the returned error on failure. At VerbosityDebug or higher the output is
// also teed to params.Stdout/Stderr so the user sees the live --debug stream.
func configureHelmDeployCmdOutput(cmd *exec.Cmd, params HelmDeployParams) (*helmOutputCapture, *strings.Builder) {
	capture := &helmOutputCapture{stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}
	if params.Verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(params.Stdout, capture.stdout)
	} else {
		cmd.Stdout = capture.stdout
	}
	stderr := new(strings.Builder)
	stderrWriters := []io.Writer{stderr, capture.stderr}
	if params.Verbosity >= core.VerbosityDebug && params.Stderr != nil {
		stderrWriters = append(stderrWriters, params.Stderr)
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)
	return capture, stderr
}

// helmInterruptGrace is how long a helm the pod watcher has interrupted is
// given to exit on its own before its process tree is killed.
//
// The grace exists because a killed helm leaves the release behind: helm
// records the upgrade as pending-upgrade before it applies anything, and its
// signal handler is what writes the terminal status back. A helm killed
// outright therefore strands the release in a state that holds helm's lock,
// and every later deploy of that environment fails against it until an
// operator clears the metadata. Observed live: `helm upgrade --wait` killed
// hard left `STATUS: pending-upgrade` behind, while the same command sent
// SIGINT released it and recorded `STATUS: failed`.
//
// The abort still has to be prompt, because it fires on a container failure
// that will not recover (a rejected image, a crash loop) and the deploy is
// waiting on it. Fifteen seconds is far longer than helm's own shutdown takes
// in the observed case and far shorter than the rollout timeout the abort
// exists to beat, so the wait is bounded by the grace and not by helm.
const helmInterruptGrace = 15 * time.Second

// interruptHelmDeploy asks the running helm upgrade to stop and waits for it,
// escalating to killing its process tree only once the grace has elapsed. It
// returns helm's own error, or the wait error if helm had to be killed.
func interruptHelmDeploy(cmd *exec.Cmd, helmDone <-chan error, grace time.Duration) error {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case err := <-helmDone:
		return err
	case <-time.After(grace):
	}
	core.TerminateProcessTree(cmd)
	return <-helmDone
}

// runHelmDeployWithPodWatch runs the already-started helm command alongside the
// release pod watcher and returns once both finish. If the watcher reports an
// early container failure before helm exits, helm is interrupted (then killed
// after a grace period) so the deploy fails fast instead of waiting out the
// helm timeout.
func runHelmDeployWithPodWatch(cmd *exec.Cmd, params HelmDeployParams) (podWatchOutcome, error) {
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	watchDone := make(chan podWatchOutcome, 1)
	go func() {
		watchDone <- watchReleasePods(watchCtx, podWatchParams{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			StatusOut:         params.Stderr,
		})
	}()

	helmDone := make(chan error, 1)
	go func() { helmDone <- cmd.Wait() }()

	var (
		helmErr       error
		watchOutcome  podWatchOutcome
		helmFinished  bool
		watchFinished bool
	)
	for !helmFinished || !watchFinished {
		select {
		case helmErr = <-helmDone:
			helmFinished = true
			cancelWatch()
		case watchOutcome = <-watchDone:
			watchFinished = true
			if watchOutcome.Failure != nil && !helmFinished {
				helmErr = interruptHelmDeploy(cmd, helmDone, helmInterruptGrace)
				helmFinished = true
				cancelWatch()
			}
		}
	}
	return watchOutcome, helmErr
}

func DeployHelmChart(params HelmDeployParams) error {
	chartPath, cleanup, err := resolveHelmDeployChartPath(params)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	command := helmDeployCommandSpec(params, chartPath)
	cmd := core.Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	helmOutput, stderr := configureHelmDeployCmdOutput(cmd, params)

	if err := cmd.Start(); err != nil {
		return err
	}

	watchOutcome, helmErr := runHelmDeployWithPodWatch(cmd, params)
	return classifyHelmDeployResult(params, watchOutcome, helmErr, helmOutput, stderr)
}

// classifyHelmDeployResult turns the raw helm/pod-watch outcome into the final
// error: an early container failure wins over a helm error, then the
// pending-operation and published-chart-not-found stderr cases are recognized,
// and otherwise the helm error is returned with its captured output appended
// (at non-debug verbosity, where the stream was silenced).
func classifyHelmDeployResult(params HelmDeployParams, watchOutcome podWatchOutcome, helmErr error, helmOutput *helmOutputCapture, stderr *strings.Builder) error {
	if watchOutcome.Failure != nil {
		failure := watchOutcome.Failure
		failure.Err = helmErr
		return failure
	}
	if helmErr == nil {
		return nil
	}
	// A rollout the deadline ended while the image was still downloading is
	// reported as that, not as helm's own words for it: "Progress deadline
	// exceeded" reads as a rollout that failed, and was in fact a rollout that
	// was still working. The pod watcher is the only component that saw which
	// one it was.
	if pulling := watchOutcome.Pulling; len(pulling) > 0 {
		return fmt.Errorf("%w\n%s", helmErr, rolloutRanOutOfWaitWhilePulling(pulling))
	}
	// The string matches below read the dedicated stderr capture, not
	// helmOutput: helm errors land on stderr, and the classification patterns
	// below are stderr-specific messages that helmOutput's combined stdout
	// content would never match.
	if isHelmReleasePendingOperationMessage(stderr.String()) {
		return &HelmReleasePendingOperationError{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			Tenant:            params.Tenant,
			Environment:       params.Environment,
			Message:           stderr.String(),
			Err:               helmErr,
		}
	}
	if deployment := immutableSelectorConflictDeployment(stderr.String()); deployment != "" {
		return &HelmImmutableSelectorError{Deployment: deployment, Err: helmErr}
	}
	if isOCIChartReference(params.ChartPath) && isHelmChartNotFoundMessage(stderr.String()) {
		return &PublishedChartNotFoundError{
			ChartReference: params.ChartPath,
			Version:        params.Version,
			Registry:       params.ContainerRegistry,
			Candidates:     params.ChartCandidates,
			TenantChart:    searchedTenantRuntimeChart(params),
			HelmOutput:     strings.TrimSpace(stderr.String()),
			Err:            helmErr,
		}
	}
	if params.Verbosity < core.VerbosityDebug {
		if output := strings.TrimSpace(helmOutput.combined()); output != "" {
			return fmt.Errorf("%w\n%s", helmErr, output)
		}
	}
	return helmErr
}

// rolloutRanOutOfWaitWhilePulling explains a rollout the wait ended mid-pull.
// It states the container, that the wait and not the workload is what ended the
// deploy, and what that costs here: every runtime chart renders
// `strategy: Recreate` (the RWO home and docker-state claims leave no choice),
// so the previous pod was torn down before the replacement was scheduled and
// the environment has nothing running until the pull finishes. It deliberately
// does not prescribe the remedy -- a longer `deploy.timeout`, a retry against a
// now-warm node, or a rollback are all real answers, and which one applies
// depends on whether the registry or the link was the slow part.
func rolloutRanOutOfWaitWhilePulling(pulling []string) string {
	return fmt.Sprintf(
		"the rollout wait expired while %s was still pulling its image: this is the deploy's own timeout ending the rollout, not a container failure. Every runtime chart replaces its pod with the Recreate strategy, so the previous pod was already torn down and this environment is running no pod until the image finishes and the replacement starts. Raise `deploy.timeout` (or pass --rollout-timeout) if a cold pull of this image legitimately needs longer than the current bound.",
		strings.Join(pulling, ", "))
}

// searchedTenantRuntimeChart names the tenant's own runtime umbrella when the
// failed pull was a searched runtime chart, so the error can offer publishing it
// as one way out. A component chart or a stated chart was never searched, and
// the erun tenant's umbrella IS the shared chart, so both yield "".
func searchedTenantRuntimeChart(params HelmDeployParams) string {
	if len(params.ChartCandidates) == 0 {
		return ""
	}
	if chart := configpkg.RuntimeReleaseName(params.Tenant); chart != configpkg.DevopsComponentName {
		return chart
	}
	return ""
}

// isHelmChartNotFoundMessage reports whether helm's captured output indicates
// the chart reference could not be resolved in the registry (a missing or
// pruned OCI tag), as opposed to a rollout, values, or connectivity failure.
// Scoped by the caller to OCI charts, it turns the opaque chart-pull exit
// status into an actionable PublishedChartNotFoundError.
func isHelmChartNotFoundMessage(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "manifest unknown") ||
		strings.Contains(lower, "manifestunknown")
}

// tracePodWatchAction records the watcher action in the dry-run trace so the
// --dry-run contract holds: every action a real run would take must appear
// in the trace. The watcher itself only fires in real-run mode (DeployHelmChart
// runs after RunHelmDeploy's DryRun early-return), so under --dry-run the
// wording stays conditional ("would watch") rather than asserting a watch that
// never started.
func tracePodWatchAction(ctx core.Context, releaseName, namespace, kubernetesContext string) {
	releaseName = strings.TrimSpace(releaseName)
	namespace = strings.TrimSpace(namespace)
	if releaseName == "" || namespace == "" {
		return
	}
	verb := "watching"
	if ctx.DryRun {
		verb = "would watch"
	}
	descriptor := "deploy: " + verb + " pods in " + namespace
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		descriptor += " on context " + c
	}
	descriptor += " for early container failure (release " + releaseName + ")"
	ctx.Trace(descriptor)
	args := []string{}
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", namespace, "get", "pods", "-o", "json")
	ctx.TraceCommand("", "kubectl", args...)
}

func ClearHelmReleasePendingOperation(params HelmReleaseRecoveryParams) error {
	if strings.TrimSpace(params.ReleaseName) == "" {
		return fmt.Errorf("helm release name is required")
	}
	if strings.TrimSpace(params.Namespace) == "" {
		return fmt.Errorf("helm release namespace is required")
	}

	command := params.Command()
	cmd := core.Command(command.Name, command.Args...)
	capture := new(bytes.Buffer)
	if params.Verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(params.Stdout, capture)
		cmd.Stderr = core.TeeWriter(params.Stderr, capture)
	} else {
		cmd.Stdout = capture
		cmd.Stderr = capture
	}
	if err := cmd.Run(); err != nil {
		if params.Verbosity < core.VerbosityDebug {
			if output := strings.TrimSpace(capture.String()); output != "" {
				return fmt.Errorf("%w\n%s", err, output)
			}
		}
		return err
	}
	return nil
}

func isHelmReleasePendingOperationMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "another operation") &&
		strings.Contains(message, "install/upgrade/rollback") &&
		strings.Contains(message, "in progress")
}

// runHelmUpgradeWithSelectorRecovery runs the helm upgrade once and, when it
// fails because a Deployment's immutable spec.selector changed, recreates that
// Deployment and retries the upgrade a single time. helm cannot patch an
// immutable selector (e.g. an env first installed under the per-tenant chart
// that labelled pods app=<release>, now upgraded by a chart that renders a
// different selector), so the only fix is delete-and-recreate. The Deployment's
// PVCs are separate objects and survive, so build cache and /home/erun are
// preserved.
func runHelmUpgradeWithSelectorRecovery(ctx core.Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, target string) error {
	spinner := core.StartSpinner(ctx.Stderr, "deploying "+target)
	deployErr := deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	var immutableSelector *HelmImmutableSelectorError
	if errors.As(deployErr, &immutableSelector) {
		return recreateDeploymentAndRetry(ctx, deployInput, deploy, immutableSelector.Deployment)
	}
	return deployErr
}

// recreateDeploymentAndRetry deletes the Deployment whose immutable selector
// blocked the helm upgrade and retries the upgrade once. The delete removes only
// the Deployment; the release's PVCs (home, docker, worktree) are separate
// objects, so the recreated pod re-mounts the same data.
func recreateDeploymentAndRetry(ctx core.Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, deployment string) error {
	ctx.Trace("deploy: Deployment " + deployment + " selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade")
	command := deleteDeploymentCommand(deployment, deployInput.Namespace, deployInput.KubernetesContext)
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if err := runDeleteDeploymentCommand(command, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("recreate deployment %s: %w", deployment, err)
	}
	spinner := core.StartSpinner(ctx.Stderr, "redeploying "+deployInput.ReleaseName)
	err := deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	return err
}

func deleteDeploymentCommand(deployment, namespace, kubernetesContext string) helm.CommandSpec {
	args := []string{}
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", namespace, "delete", "deployment", deployment, "--ignore-not-found")
	return helm.CommandSpec{Name: "kubectl", Args: args}
}

func runDeleteDeploymentCommand(command helm.CommandSpec, verbosity int, stdout, stderr io.Writer) error {
	cmd := core.Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	capture := new(bytes.Buffer)
	if verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(stdout, capture)
		cmd.Stderr = core.TeeWriter(stderr, capture)
	} else {
		cmd.Stdout = capture
		cmd.Stderr = capture
	}
	if err := cmd.Run(); err != nil {
		if verbosity < core.VerbosityDebug {
			if output := strings.TrimSpace(capture.String()); output != "" {
				return fmt.Errorf("%w\n%s", err, output)
			}
		}
		return err
	}
	return nil
}

var immutableSelectorDeploymentPattern = regexp.MustCompile(`(?i)Deployment\.apps "([^"]+)" is invalid`)

// immutableSelectorConflictDeployment returns the Deployment name from a helm
// upgrade failure caused by an immutable spec.selector change, or "" when the
// failure is something else. Scoped to spec.selector specifically so an
// unrelated immutable-field error never triggers a Deployment delete.
func immutableSelectorConflictDeployment(message string) string {
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "spec.selector") || !strings.Contains(lower, "field is immutable") {
		return ""
	}
	match := immutableSelectorDeploymentPattern.FindStringSubmatch(message)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
