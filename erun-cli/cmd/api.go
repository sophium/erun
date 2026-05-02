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

const defaultAPIHost = "127.0.0.1"

func newAPICmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error, launchAPI APILauncher) *cobra.Command {
	host := defaultAPIHost
	port := common.APIServicePort
	databaseURL := ""
	databaseDialect := ""
	allowedIssuers := ""

	cmd := &cobra.Command{
		Use:   "api [TENANT] [ENVIRONMENT]",
		Short: "Run ERun backend API over HTTP",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			result, _, err := resolveOpenWithInitRetry(ctx, args, shouldRunInitForOpenCommand, resolveOpen, runInitForArgs)
			if err != nil {
				return err
			}
			resolvedPort := port
			if !cmd.Flags().Changed("port") {
				resolvedPort = common.APIPortForResult(result)
			}

			commandArgs := apiCommandArgs(host, resolvedPort, databaseURL, databaseDialect, allowedIssuers)
			ctx.TraceCommand("", "eapi", commandArgs...)
			if ctx.DryRun {
				return nil
			}

			return launchAPI(ctx.Stdin, ctx.Stdout, ctx.Stderr, commandArgs)
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&host, "host", defaultAPIHost, "Host interface to bind the backend API HTTP server to")
	cmd.Flags().IntVar(&port, "port", common.APIServicePort, "Port to bind the backend API HTTP server to")
	cmd.Flags().StringVar(&databaseURL, "database-url", "", "Backend database URL; defaults to ERUN_DATABASE_URL or local SQLite")
	cmd.Flags().StringVar(&databaseDialect, "database-dialect", "", "Backend database dialect: sqlite or postgres")
	cmd.Flags().StringVar(&allowedIssuers, "oidc-allowed-issuers", "", "Comma-separated OIDC issuer allow-list")
	cmd.Example = fmt.Sprintf("  erun api --host %s --port %d\n  erun api tenant-a dev", defaultAPIHost, common.APIServicePort)
	return cmd
}

func apiCommandArgs(host string, port int, databaseURL string, databaseDialect string, allowedIssuers string) []string {
	args := []string{
		"--host", host,
		"--port", strconv.Itoa(port),
	}
	if databaseURL != "" {
		args = append(args, "--database-url", databaseURL)
	}
	if databaseDialect != "" {
		args = append(args, "--database-dialect", databaseDialect)
	}
	if allowedIssuers != "" {
		args = append(args, "--oidc-allowed-issuers", allowedIssuers)
	}
	return args
}

func launchAPIProcess(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	cmd := exec.Command(resolveAPIExecutable(), args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("eapi executable not found; build or install it first")
		}
		return err
	}
	return nil
}

func resolveAPIExecutable() string {
	executable, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(executable), "eapi")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling
		}
		devBinary := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", "erun-backend", "erun-backend-api", "bin", "eapi"))
		if info, statErr := os.Stat(devBinary); statErr == nil && !info.IsDir() {
			return devBinary
		}
	}
	return "eapi"
}
