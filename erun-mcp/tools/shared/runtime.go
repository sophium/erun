package shared

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/e2e"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type RuntimeContext struct {
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
	RepoPath          string `json:"repoPath,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
}

type RuntimeStore interface {
	setup.BootstrapStore
	lifecycle.ListStore
	shared.DockerStore
	deploy.DeployStore
}

type RuntimeConfig struct {
	Context                        RuntimeContext
	Store                          RuntimeStore
	BuildScriptRunner              build.BuildScriptRunnerFunc
	BuildDockerImage               build.DockerImageBuilderFunc
	PushDockerImage                shared.DockerImagePusherFunc
	DeployHelmChart                deploy.HelmChartDeployerFunc
	EnsureKubernetesNamespace      deploy.NamespaceEnsurerFunc
	DeleteKubernetesNamespace      lifecycle.NamespaceDeleterFunc
	WaitForRemoteRuntime           setup.RemoteRuntimeWaitFunc
	RunRemoteCommand               kube.RemoteCommandRunnerFunc
	ResolveRuntimeRegistryVersions registry.RuntimeRegistryVersionResolverFunc
}

type CommandOutput struct {
	Executed         bool                    `json:"executed"`
	WorkingDirectory string                  `json:"workingDirectory,omitempty"`
	Trace            []string                `json:"trace,omitempty"`
	Stdout           string                  `json:"stdout,omitempty"`
	Stderr           string                  `json:"stderr,omitempty"`
	RootConfig       *DoctorRootConfigReport `json:"rootConfig,omitempty"`
	// Build lets an agent thread the minted version into push/deploy.
	Build *core.BuildResult `json:"build,omitempty"`
	// Pin is the resolved re-pin plan.
	Pin                *PinOutput                               `json:"pin,omitempty"`
	Write              *gitx.WriteWorkingTreeFileResult         `json:"write,omitempty"`
	Commit             *gitx.CommitWorkingTreeResult            `json:"commit,omitempty"`
	Push               *gitx.PushWorkingTreeBranchResult        `json:"push,omitempty"`
	Merge              *mergequeue.MergeWorkingTreeBranchResult `json:"merge,omitempty"`
	GateMerge          *mergequeue.GateMergeWorkingTreeResult   `json:"gateMerge,omitempty"`
	ReportCommitStatus *review.ReportCommitStatusResult         `json:"reportCommitStatus,omitempty"`
	ClosePullRequest   *review.ClosePullRequestResult           `json:"closePullRequest,omitempty"`
	Spec               *shared.ReleaseSpec                      `json:"spec,omitempty"`
	// Interaction is a question init needs answered in a follow-up call.
	Interaction *setup.BootstrapInitInteraction `json:"interaction,omitempty"`
}

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func RuntimeRepoPath(runtime RuntimeContext) (string, error) {
	if repoPath := strings.TrimSpace(runtime.RepoPath); repoPath != "" {
		return repoPath, nil
	}
	return os.Getwd()
}

func captureCommandOutput(log io.Writer, work func(stdout, stderr io.Writer) error) (string, string, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	err := work(MirrorToJobLog(stdout, log), MirrorToJobLog(stderr, log))
	return stdout.String(), stderr.String(), err
}

// MirrorToJobLog tees output to the inline buffer and the background job's log,
// so polls show progress and failures leave a log.
func MirrorToJobLog(capture, log io.Writer) io.Writer {
	if log == nil || log == io.Discard {
		return capture
	}
	return io.MultiWriter(capture, log)
}

func RuntimePushFunc(runtime RuntimeConfig) build.DockerPushFunc {
	return func(ctx core.Context, pushInput shared.DockerPushSpec) error {
		return push.RunDockerPush(ctx, pushInput, runtime.PushDockerImage)
	}
}

func RunCommandOutput(ctx core.Context, workDir string, traceOutput *bytes.Buffer, log io.Writer, run func(core.Context) error) (CommandOutput, error) {
	if ctx.DryRun {
		if err := run(ctx); err != nil {
			return CommandOutput{}, err
		}
		return CommandOutput{
			Executed:         false,
			WorkingDirectory: workDir,
			Trace:            NormalizeTraceLines(traceOutput.String()),
		}, nil
	}

	stdout, stderr, err := captureCommandOutput(log, func(stdout, stderr io.Writer) error {
		runCtx := ctx
		runCtx.Stdout = stdout
		runCtx.Stderr = stderr
		return run(runCtx)
	})
	if err != nil {
		return CommandOutput{
			Executed:         true,
			WorkingDirectory: workDir,
			Trace:            NormalizeTraceLines(traceOutput.String()),
			Stdout:           stdout,
			Stderr:           stderr,
		}, err
	}
	return CommandOutput{
		Executed:         true,
		WorkingDirectory: workDir,
		Trace:            NormalizeTraceLines(traceOutput.String()),
		Stdout:           stdout,
		Stderr:           stderr,
	}, nil
}

// RunRuntimeCommand runs one tool's work and captures it; log is the background
// job's log, or nil for a synchronous call.
func RunRuntimeCommand(runtime RuntimeConfig, preview bool, verbosity int, log io.Writer, run func(core.Context, string) error) (CommandOutput, error) {
	traceOutput := new(bytes.Buffer)
	traceSink := MirrorToJobLog(traceOutput, log)
	ctx := RuntimeCallContext(preview, verbosity, nil, traceSink, traceSink)
	ctx.KubernetesContextPreflight = cloud.CloudContextPreflight(runtime.Store, cloud.CloudContextDependencies{})
	// The env trace feeds the desktop's Diagnostics console; read-only tools bypass
	// RunRuntimeCommand so probe polls don't flood it.
	ctx, closeEnvTrace := e2e.ActivateEnvTrace(ctx, runtime.Context.Tenant, runtime.Context.Environment)
	defer closeEnvTrace()

	workDir, err := RuntimeRepoPath(runtime.Context)
	if err != nil {
		return CommandOutput{}, err
	}

	output, err := RunCommandOutput(ctx, workDir, traceOutput, log, func(runCtx core.Context) error {
		return run(runCtx, workDir)
	})
	return output, err
}

func RuntimeFindProjectRoot(runtime RuntimeContext, workDir string) (string, string, error) {
	repoPath := strings.TrimSpace(runtime.RepoPath)
	if repoPath != "" {
		return FirstNonEmpty(strings.TrimSpace(runtime.Tenant), filepath.Base(repoPath)), filepath.Clean(repoPath), nil
	}
	return config.FindProjectRootFromDir(workDir)
}

// ResolveLocalTarget resolves the tool's tenant/environment and refuses any other
// environment: this server can only act on its own pod.
func ResolveLocalTarget(runtime RuntimeConfig, tenant, environment string) (string, string, error) {
	resolvedTenant, resolvedEnvironment, err := matchOrDefaultLocalTarget(
		strings.TrimSpace(runtime.Context.Tenant), strings.TrimSpace(runtime.Context.Environment),
		tenant, environment,
	)
	if err != nil {
		return "", "", err
	}
	if resolvedTenant == "" || resolvedEnvironment == "" {
		return "", "", ErrMissingLocalTarget(resolvedTenant == "", resolvedEnvironment == "")
	}
	return resolvedTenant, resolvedEnvironment, nil
}

// matchOrDefaultLocalTarget refuses a different requested environment, naming both
// and the remedy; an empty field defaults to the server's own.
func matchOrDefaultLocalTarget(serverTenant, serverEnvironment, requestedTenant, requestedEnvironment string) (tenant, environment string, err error) {
	requestedTenant = strings.TrimSpace(requestedTenant)
	requestedEnvironment = strings.TrimSpace(requestedEnvironment)
	tenantMismatch := requestedTenant != "" && serverTenant != "" && requestedTenant != serverTenant
	environmentMismatch := requestedEnvironment != "" && serverEnvironment != "" && requestedEnvironment != serverEnvironment
	if tenantMismatch || environmentMismatch {
		return "", "", fmt.Errorf(
			"this MCP server serves %s/%s and runs every tool there, so it cannot act on %s/%s: call that environment's own MCP edge instead",
			serverTenant, serverEnvironment,
			FirstNonEmpty(requestedTenant, serverTenant), FirstNonEmpty(requestedEnvironment, serverEnvironment),
		)
	}
	return FirstNonEmpty(requestedTenant, serverTenant), FirstNonEmpty(requestedEnvironment, serverEnvironment), nil
}

// ErrMissingLocalTarget names what is missing and how to recover.
func ErrMissingLocalTarget(missingTenant, missingEnvironment bool) error {
	var missing []string
	if missingTenant {
		missing = append(missing, "tenant")
	}
	if missingEnvironment {
		missing = append(missing, "environment")
	}
	what := strings.Join(missing, "/")
	return fmt.Errorf(
		"%s not resolved: this MCP server was not started bound to a %s, and the call did not supply %s either -- pass %s explicitly in the call, or run this edge for an environment that has it configured",
		what, what, what, strings.Join(missing, " and "),
	)
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func RuntimeCallContext(preview bool, verbosity int, stdin io.Reader, stdout, stderr io.Writer) core.Context {
	if preview && verbosity < core.VerbosityTrace {
		verbosity = core.VerbosityTrace
	}
	if verbosity > core.VerbosityTrace {
		verbosity = core.VerbosityTrace
	}
	return core.Context{
		Logger:    core.NewLoggerWithWriters(verbosity, stderr, stderr),
		Verbosity: verbosity,
		DryRun:    preview,
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
	}
}

func NormalizeTraceLines(values ...string) []string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(ansiRegexp.ReplaceAllString(line, ""))
			if line == "" {
				continue
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}
