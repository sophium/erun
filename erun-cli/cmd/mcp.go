package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

const (
	defaultMCPHost = "127.0.0.1"
	defaultMCPPort = 17000
	defaultMCPPath = "/mcp"
)

type mcpLaunchContext struct {
	Tenant            string
	Environment       string
	RepoPath          string
	KubernetesContext string
	Namespace         string
}

func newMCPCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error, launchMCP MCPLauncher) *cobra.Command {
	host := defaultMCPHost
	port := defaultMCPPort
	path := defaultMCPPath

	cmd := &cobra.Command{
		Use:   "mcp [TENANT] [ENVIRONMENT]",
		Short: "Run ERun as an MCP server over HTTP",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			result, _, err := resolveOpenWithInitRetry(ctx, args, shouldRunInitForOpenCommand, resolveOpen, runInitForArgs)
			if err != nil {
				return err
			}

			runtime := mcpLaunchContext{
				Tenant:            result.Tenant,
				Environment:       result.Environment,
				RepoPath:          result.RepoPath,
				KubernetesContext: result.EnvConfig.KubernetesContext,
				Namespace:         common.KubernetesNamespaceName(result.Tenant, result.Environment),
			}
			commandArgs := mcpCommandArgs(host, port, path, runtime)
			ctx.TraceCommand("", "emcp", commandArgs...)
			if ctx.DryRun {
				return nil
			}

			return launchMCP(ctx.Stdin, ctx.Stdout, ctx.Stderr, commandArgs)
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&host, "host", defaultMCPHost, "Host interface to bind the MCP HTTP server to")
	cmd.Flags().IntVar(&port, "port", defaultMCPPort, "Port to bind the MCP HTTP server to")
	cmd.Flags().StringVar(&path, "path", defaultMCPPath, "HTTP path to serve the MCP endpoint from")
	cmd.Example = fmt.Sprintf("  erun mcp --host %s --port %d --path %s\n  erun mcp tenant-a dev", defaultMCPHost, defaultMCPPort, defaultMCPPath)
	return cmd
}

func mcpCommandArgs(host string, port int, path string, runtime mcpLaunchContext) []string {
	args := []string{
		"--host", host,
		"--port", strconv.Itoa(port),
		"--path", path,
	}
	if runtime.Tenant != "" {
		args = append(args, "--tenant", runtime.Tenant)
	}
	if runtime.Environment != "" {
		args = append(args, "--environment", runtime.Environment)
	}
	if runtime.RepoPath != "" {
		args = append(args, "--repo-path", runtime.RepoPath)
	}
	if runtime.KubernetesContext != "" {
		args = append(args, "--kubernetes-context", runtime.KubernetesContext)
	}
	if runtime.Namespace != "" {
		args = append(args, "--namespace", runtime.Namespace)
	}
	return args
}

func launchMCPProcess(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	cmd := exec.Command(resolveMCPExecutable(), args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("emcp executable not found; build or install it first")
		}
		return err
	}
	return nil
}

func resolveMCPExecutable() string {
	executable, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(executable), "emcp")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling
		}
		devBinary := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", "erun-mcp", "bin", "emcp"))
		if info, statErr := os.Stat(devBinary); statErr == nil && !info.IsDir() {
			return devBinary
		}
	}
	return "emcp"
}
