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
	WaitForRemoteRuntime      eruncommon.RemoteRuntimeWaitFunc
	RunRemoteCommand          eruncommon.RemoteCommandRunnerFunc
}

type CommandOutput struct {
	Executed         bool     `json:"executed"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
	Trace            []string `json:"trace,omitempty"`
	Stdout           string   `json:"stdout,omitempty"`
	Stderr           string   `json:"stderr,omitempty"`
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
	if cfg.WaitForRemoteRuntime == nil {
		cfg.WaitForRemoteRuntime = eruncommon.WaitForShellDeployment
	}
	if cfg.RunRemoteCommand == nil {
		cfg.RunRemoteCommand = eruncommon.RunRemoteCommand
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

func runRuntimeCommand(runtime RuntimeContext, preview bool, verbosity int, run func(eruncommon.Context, string) error) (CommandOutput, error) {
	traceOutput := new(bytes.Buffer)
	ctx := runtimeCallContext(preview, verbosity, nil, traceOutput, traceOutput)

	workDir, err := runtimeRepoPath(runtime)
	if err != nil {
		return CommandOutput{}, err
	}

	output, err := runCommandOutput(ctx, workDir, traceOutput, func(runCtx eruncommon.Context) error {
		return run(runCtx, workDir)
	})
	return output, err
}

func resolveRuntimeOpenResult(runtime RuntimeConfig) (eruncommon.OpenResult, error) {
	tenant := strings.TrimSpace(runtime.Context.Tenant)
	environment := strings.TrimSpace(runtime.Context.Environment)
	if tenant == "" || environment == "" {
		return eruncommon.OpenResult{}, fmt.Errorf("server context is not configured; start emcp through `erun mcp [tenant] [environment]` or pass context flags")
	}

	repoPath := strings.TrimSpace(runtime.Context.RepoPath)
	kubernetesContext := strings.TrimSpace(runtime.Context.KubernetesContext)
	if repoPath != "" && kubernetesContext != "" {
		return eruncommon.OpenResult{
			Tenant:      tenant,
			Environment: environment,
			TenantConfig: eruncommon.TenantConfig{
				Name:               tenant,
				ProjectRoot:        repoPath,
				DefaultEnvironment: environment,
			},
			EnvConfig: eruncommon.EnvConfig{
				Name:              environment,
				RepoPath:          repoPath,
				KubernetesContext: kubernetesContext,
			},
			RepoPath: repoPath,
			Title:    tenant + "-" + environment,
		}, nil
	}

	return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{
		Tenant:      tenant,
		Environment: environment,
	})
}

func runtimeFindProjectRoot(runtime RuntimeContext, workDir string) (string, string, error) {
	repoPath := strings.TrimSpace(runtime.RepoPath)
	if repoPath != "" {
		return firstNonEmpty(strings.TrimSpace(runtime.Tenant), filepath.Base(repoPath)), filepath.Clean(repoPath), nil
	}
	return eruncommon.FindProjectRootFromDir(workDir)
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
	return eruncommon.Context{
		Logger: eruncommon.NewLoggerWithWriters(eruncommon.TraceLoggerVerbosity(verbosity), stderr, stderr),
		DryRun: preview,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
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
