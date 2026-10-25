package helmrun

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

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartfiles"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/recovery"
)

// resolveHelmDeployChartPath returns the chart path and an optional cleanup; an
// OCI chart is pinned on the command line instead of stamped locally.
func resolveHelmDeployChartPath(params helmdeploy.HelmDeployParams) (string, func(), error) {
	if strings.TrimSpace(params.Version) == "" || chartref.IsOCIChartReference(params.ChartPath) {
		return params.ChartPath, nil, nil
	}
	chartPath, cleanup, err := chartfiles.PrepareHelmChartForDeploy(params.ChartPath, params.Version)
	if err != nil {
		return "", nil, err
	}
	return chartPath, cleanup, nil
}

func helmDeployCommandSpec(params helmdeploy.HelmDeployParams, chartPath string) helm.CommandSpec {
	// Built from the traced spec so the executed command matches the trace.
	spec := params.HelmDeploySpec
	spec.ChartPath = chartPath
	return HelmDeployCommand(spec)
}

// helmOutputCapture keeps separate buffers: os/exec only aliases stdout and
// stderr when they are the same writer, so one buffer would be written by two
// copier goroutines. combined() is safe only after cmd.Wait() returns.
type helmOutputCapture struct {
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (c *helmOutputCapture) combined() string {
	return c.stdout.String() + c.stderr.String()
}

// configureHelmDeployCmdOutput captures helm output silently, teeing it live at
// debug verbosity.
func configureHelmDeployCmdOutput(cmd *exec.Cmd, params helmdeploy.HelmDeployParams) (*helmOutputCapture, *strings.Builder) {
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

// helmInterruptGrace bounds how long an interrupted helm may exit on its own:
// helm's SIGINT handler writes the release status back, while a hard kill leaves
// it locked in pending-upgrade.
const helmInterruptGrace = 15 * time.Second

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

// runHelmDeployWithPodWatch interrupts helm when the pod watcher reports an early
// container failure, so the deploy fails fast.
func runHelmDeployWithPodWatch(cmd *exec.Cmd, params helmdeploy.HelmDeployParams) (podWatchOutcome, error) {
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

func DeployHelmChart(params helmdeploy.HelmDeployParams) error {
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

func classifyHelmDeployResult(params helmdeploy.HelmDeployParams, watchOutcome podWatchOutcome, helmErr error, helmOutput *helmOutputCapture, stderr *strings.Builder) error {
	if watchOutcome.Failure != nil {
		failure := watchOutcome.Failure
		failure.Err = helmErr
		return failure
	}
	if helmErr == nil {
		return nil
	}
	// Only the pod watcher saw that the deadline hit a still-downloading image;
	// helm's "Progress deadline exceeded" reads as a failed rollout.
	if pulling := watchOutcome.Pulling; len(pulling) > 0 {
		return fmt.Errorf("%w\n%s", helmErr, rolloutRanOutOfWaitWhilePulling(pulling))
	}
	// Classify on stderr only: these messages never appear on stdout.
	if recovery.IsHelmReleasePendingOperationMessage(stderr.String()) {
		return &recovery.HelmReleasePendingOperationError{
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
	if chartref.IsOCIChartReference(params.ChartPath) && isHelmChartNotFoundMessage(stderr.String()) {
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

// rolloutRanOutOfWaitWhilePulling deliberately prescribes no single remedy.
func rolloutRanOutOfWaitWhilePulling(pulling []string) string {
	return fmt.Sprintf(
		"the rollout wait expired while %s was still pulling its image: this is the deploy's own timeout ending the rollout, not a container failure. Every runtime chart replaces its pod with the Recreate strategy, so the previous pod was already torn down and this environment is running no pod until the image finishes and the replacement starts. Raise `deploy.timeout` (or pass --rollout-timeout) if a cold pull of this image legitimately needs longer than the current bound.",
		strings.Join(pulling, ", "))
}

// searchedTenantRuntimeChart returns "" for an unsearched chart and for the erun
// tenant, whose umbrella is the shared chart.
func searchedTenantRuntimeChart(params helmdeploy.HelmDeployParams) string {
	if len(params.ChartCandidates) == 0 {
		return ""
	}
	if chart := runtimetarget.RuntimeReleaseName(params.Tenant); chart != runtimetarget.DevopsComponentName {
		return chart
	}
	return ""
}

func isHelmChartNotFoundMessage(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "manifest unknown") ||
		strings.Contains(lower, "manifestunknown")
}

// TracePodWatchAction keeps --dry-run's trace complete; the watcher itself only
// runs for real.
func TracePodWatchAction(ctx core.Context, releaseName, namespace, kubernetesContext string) {
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

// RunHelmUpgradeWithSelectorRecovery recreates a Deployment whose immutable
// spec.selector changed and retries once; its PVCs survive the delete.
func RunHelmUpgradeWithSelectorRecovery(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, deploy helmdeploy.HelmChartDeployerFunc, target string) error {
	spinner := core.StartSpinner(ctx.Stderr, "deploying "+target)
	deployErr := deploy(HelmDeployParamsFor(deployInput, ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	var immutableSelector *HelmImmutableSelectorError
	if errors.As(deployErr, &immutableSelector) {
		return recreateDeploymentAndRetry(ctx, deployInput, deploy, immutableSelector.Deployment)
	}
	return deployErr
}

func recreateDeploymentAndRetry(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, deploy helmdeploy.HelmChartDeployerFunc, deployment string) error {
	ctx.Trace("deploy: Deployment " + deployment + " selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade")
	command := deleteDeploymentCommand(deployment, deployInput.Namespace, deployInput.KubernetesContext)
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if err := runDeleteDeploymentCommand(command, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("recreate deployment %s: %w", deployment, err)
	}
	spinner := core.StartSpinner(ctx.Stderr, "redeploying "+deployInput.ReleaseName)
	err := deploy(HelmDeployParamsFor(deployInput, ctx.Stdout, ctx.Stderr))
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

// immutableSelectorConflictDeployment matches spec.selector only, so other
// immutable-field errors never trigger a Deployment delete.
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
