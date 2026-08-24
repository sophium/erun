package erunmcp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

type RuntimeContext struct {
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
	RepoPath          string `json:"repoPath,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
}

type runtimeStore interface {
	eruncommon.BootstrapStore
	eruncommon.ListStore
	eruncommon.DockerStore
	eruncommon.DeployStore
}

type RuntimeConfig struct {
	Context                   RuntimeContext
	Store                     runtimeStore
	BuildScriptRunner         eruncommon.BuildScriptRunnerFunc
	BuildDockerImage          eruncommon.DockerImageBuilderFunc
	PushDockerImage           eruncommon.DockerImagePusherFunc
	DeployHelmChart           eruncommon.HelmChartDeployerFunc
	EnsureKubernetesNamespace eruncommon.NamespaceEnsurerFunc
	DeleteKubernetesNamespace eruncommon.NamespaceDeleterFunc
	WaitForRemoteRuntime      eruncommon.RemoteRuntimeWaitFunc
	RunRemoteCommand          eruncommon.RemoteCommandRunnerFunc
}

type CommandOutput struct {
	Executed         bool                    `json:"executed"`
	WorkingDirectory string                  `json:"workingDirectory,omitempty"`
	Trace            []string                `json:"trace,omitempty"`
	Stdout           string                  `json:"stdout,omitempty"`
	Stderr           string                  `json:"stderr,omitempty"`
	RootConfig       *DoctorRootConfigReport `json:"rootConfig,omitempty"`
	// Build lets an Agent capture the minted version and thread it into `push`
	// / `deploy`, since MCP composes the pure primitives itself.
	Build *eruncommon.BuildResult `json:"build,omitempty"`
	// Pin carries the resolved re-pin plan so a caller sees every reference that
	// moved without diffing the tree afterwards.
	Pin *PinOutput `json:"pin,omitempty"`
	// Write carries what a `write` call actually wrote.
	Write *eruncommon.WriteWorkingTreeFileResult `json:"write,omitempty"`
	// Commit carries what a `commit` call actually committed.
	Commit *eruncommon.CommitWorkingTreeResult `json:"commit,omitempty"`
}

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func normalizeRuntimeConfig(cfg RuntimeConfig) RuntimeConfig {
	if cfg.Store == nil {
		cfg.Store = eruncommon.ConfigStore{}
	}
	if cfg.BuildScriptRunner == nil {
		cfg.BuildScriptRunner = eruncommon.BuildScriptRunner
	}
	if cfg.BuildDockerImage == nil {
		cfg.BuildDockerImage = eruncommon.DockerImageBuilder
	}
	if cfg.PushDockerImage == nil {
		cfg.PushDockerImage = eruncommon.DockerImagePusher
	}
	if cfg.DeployHelmChart == nil {
		cfg.DeployHelmChart = eruncommon.DeployHelmChart
	}
	namespaceEnsurer := cfg.EnsureKubernetesNamespace
	if namespaceEnsurer == nil {
		namespaceEnsurer = eruncommon.EnsureKubernetesNamespace
	}
	cfg.DeployHelmChart = eruncommon.WrapHelmChartDeployerWithNamespaceEnsure(namespaceEnsurer, cfg.DeployHelmChart)
	if cfg.WaitForRemoteRuntime == nil {
		cfg.WaitForRemoteRuntime = eruncommon.WaitForShellDeployment
	}
	if cfg.RunRemoteCommand == nil {
		cfg.RunRemoteCommand = eruncommon.RunRemoteCommand
	}
	if cfg.DeleteKubernetesNamespace == nil {
		cfg.DeleteKubernetesNamespace = eruncommon.DeleteKubernetesNamespace
	}
	return cfg
}

func runtimeRepoPath(runtime RuntimeContext) (string, error) {
	if repoPath := strings.TrimSpace(runtime.RepoPath); repoPath != "" {
		return repoPath, nil
	}
	return os.Getwd()
}

func captureCommandOutput(work func(stdout, stderr io.Writer) error) (string, string, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	err := work(stdout, stderr)
	return stdout.String(), stderr.String(), err
}

func runtimePushFunc(runtime RuntimeConfig) eruncommon.DockerPushFunc {
	return func(ctx eruncommon.Context, pushInput eruncommon.DockerPushSpec) error {
		return eruncommon.RunDockerPush(ctx, pushInput, runtime.PushDockerImage)
	}
}

func runCommandOutput(ctx eruncommon.Context, workDir string, traceOutput *bytes.Buffer, run func(eruncommon.Context) error) (CommandOutput, error) {
	if ctx.DryRun {
		if err := run(ctx); err != nil {
			return CommandOutput{}, err
		}
		return CommandOutput{
			Executed:         false,
			WorkingDirectory: workDir,
			Trace:            normalizeTraceLines(traceOutput.String()),
		}, nil
	}

	stdout, stderr, err := captureCommandOutput(func(stdout, stderr io.Writer) error {
		runCtx := ctx
		runCtx.Stdout = stdout
		runCtx.Stderr = stderr
		return run(runCtx)
	})
	if err != nil {
		return CommandOutput{
			Executed:         true,
			WorkingDirectory: workDir,
			Trace:            normalizeTraceLines(traceOutput.String()),
			Stdout:           stdout,
			Stderr:           stderr,
		}, err
	}
	return CommandOutput{
		Executed:         true,
		WorkingDirectory: workDir,
		Trace:            normalizeTraceLines(traceOutput.String()),
		Stdout:           stdout,
		Stderr:           stderr,
	}, nil
}

func runRuntimeCommand(runtime RuntimeConfig, preview bool, verbosity int, run func(eruncommon.Context, string) error) (CommandOutput, error) {
	traceOutput := new(bytes.Buffer)
	ctx := runtimeCallContext(preview, verbosity, nil, traceOutput, traceOutput)
	ctx.KubernetesContextPreflight = eruncommon.CloudContextPreflight(runtime.Store, eruncommon.CloudContextDependencies{})
	// Surfaces agent-driven operations in the desktop's Diagnostics console,
	// matching the CLI's trace contract. Read-only tools (idle, raw, list,
	// version) deliberately bypass runRuntimeCommand so probe polls cannot
	// flood the trace log.
	ctx, closeEnvTrace := eruncommon.ActivateEnvTrace(ctx, runtime.Context.Tenant, runtime.Context.Environment)
	defer closeEnvTrace()

	workDir, err := runtimeRepoPath(runtime.Context)
	if err != nil {
		return CommandOutput{}, err
	}

	output, err := runCommandOutput(ctx, workDir, traceOutput, func(runCtx eruncommon.Context) error {
		return run(runCtx, workDir)
	})
	return output, err
}

func runtimeFindProjectRoot(runtime RuntimeContext, workDir string) (string, string, error) {
	repoPath := strings.TrimSpace(runtime.RepoPath)
	if repoPath != "" {
		return firstNonEmpty(strings.TrimSpace(runtime.Tenant), filepath.Base(repoPath)), filepath.Clean(repoPath), nil
	}
	return eruncommon.FindProjectRootFromDir(workDir)
}

// resolveLocalTarget resolves the tenant/environment a tool acts on, and refuses
// a target this server cannot reach. An MCP server serves exactly one
// environment: its tools run in this pod, against this pod's repo and this pod's
// erun binary, so an explicit tenant/environment naming a DIFFERENT environment
// can never be honoured. Accepting one and acting locally anyway is worse than
// refusing it, because the result then asserts a target the work never reached
// and the caller is left holding written evidence that it worked (#1195).
func resolveLocalTarget(runtime RuntimeConfig, tenant, environment string) (string, string, error) {
	serverTenant := strings.TrimSpace(runtime.Context.Tenant)
	serverEnvironment := strings.TrimSpace(runtime.Context.Environment)
	requestedTenant := strings.TrimSpace(tenant)
	requestedEnvironment := strings.TrimSpace(environment)
	tenantMismatch := requestedTenant != "" && serverTenant != "" && requestedTenant != serverTenant
	environmentMismatch := requestedEnvironment != "" && serverEnvironment != "" && requestedEnvironment != serverEnvironment
	if tenantMismatch || environmentMismatch {
		return "", "", fmt.Errorf(
			"this MCP server serves %s/%s and runs every tool there, so it cannot act on %s/%s: call that environment's own MCP edge instead",
			serverTenant, serverEnvironment,
			firstNonEmpty(requestedTenant, serverTenant), firstNonEmpty(requestedEnvironment, serverEnvironment),
		)
	}
	resolvedTenant := firstNonEmpty(requestedTenant, serverTenant)
	resolvedEnvironment := firstNonEmpty(requestedEnvironment, serverEnvironment)
	if resolvedTenant == "" || resolvedEnvironment == "" {
		return "", "", fmt.Errorf("tenant and environment are required")
	}
	return resolvedTenant, resolvedEnvironment, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func runtimeCallContext(preview bool, verbosity int, stdin io.Reader, stdout, stderr io.Writer) eruncommon.Context {
	if preview && verbosity < eruncommon.VerbosityTrace {
		verbosity = eruncommon.VerbosityTrace
	}
	if verbosity > eruncommon.VerbosityTrace {
		verbosity = eruncommon.VerbosityTrace
	}
	return eruncommon.Context{
		Logger:    eruncommon.NewLoggerWithWriters(verbosity, stderr, stderr),
		Verbosity: verbosity,
		DryRun:    preview,
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
	}
}

func normalizeTraceLines(values ...string) []string {
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
