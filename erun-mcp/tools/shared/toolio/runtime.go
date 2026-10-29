package toolio

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

type RuntimeContext struct {
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
	RepoPath          string `json:"repoPath,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
}

type CommandOutput struct {
	Executed         bool                    `json:"executed"`
	WorkingDirectory string                  `json:"workingDirectory,omitempty"`
	Trace            []string                `json:"trace,omitempty"`
	Stdout           string                  `json:"stdout,omitempty"`
	Stderr           string                  `json:"stderr,omitempty"`
	RootConfig       *DoctorRootConfigReport `json:"rootConfig,omitempty"`
	// Build lets an agent thread the minted version into push/deploy.
	Build *model.BuildResult `json:"build,omitempty"`
	// Pin is the resolved re-pin plan.
	Pin                *PinOutput                          `json:"pin,omitempty"`
	Write              *model.WriteWorkingTreeFileResult   `json:"write,omitempty"`
	Commit             *model.CommitWorkingTreeResult      `json:"commit,omitempty"`
	Push               *model.PushWorkingTreeBranchResult  `json:"push,omitempty"`
	Merge              *model.MergeWorkingTreeBranchResult `json:"merge,omitempty"`
	GateMerge          *model.GateMergeWorkingTreeResult   `json:"gateMerge,omitempty"`
	ReportCommitStatus *model.ReportCommitStatusResult     `json:"reportCommitStatus,omitempty"`
	ClosePullRequest   *model.ClosePullRequestResult       `json:"closePullRequest,omitempty"`
	Spec               *model.ReleaseSpec                  `json:"spec,omitempty"`
	// Interaction is a question init needs answered in a follow-up call.
	Interaction *model.BootstrapInitInteraction `json:"interaction,omitempty"`
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

func RuntimeFindProjectRoot(runtime RuntimeContext, workDir string) (string, string, error) {
	repoPath := strings.TrimSpace(runtime.RepoPath)
	if repoPath != "" {
		return FirstNonEmpty(strings.TrimSpace(runtime.Tenant), filepath.Base(repoPath)), filepath.Clean(repoPath), nil
	}
	return projectroot.FindProjectRootFromDir(workDir)
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
