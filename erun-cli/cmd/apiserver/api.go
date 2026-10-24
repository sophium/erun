package apiserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

const defaultAPIHost = "127.0.0.1"

func NewAPICmd(resolveOpen func(sharedpkg.OpenParams) (config.OpenResult, error), runInitForArgs func(core.Context, []string) error, launchAPI shared.APILauncher) *cobra.Command {
	host := defaultAPIHost
	port := config.APIServicePort
	databaseURL := ""
	allowedIssuers := ""
	awsIdentityStoreID := ""
	awsIdentityStoreRegion := ""

	cmd := &cobra.Command{
		Use:   "api [TENANT] [ENVIRONMENT]",
		Short: "Run ERun backend API over HTTP",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			result, _, err := shared.ResolveOpenWithInitRetry(ctx, args, shared.ShouldRunInitForOpenCommand, resolveOpen, runInitForArgs)
			if err != nil {
				return err
			}
			resolvedPort := port
			if !cmd.Flags().Changed("port") {
				resolvedPort = config.APIPortForResult(result)
			}

			commandArgs := apiCommandArgs(host, resolvedPort, databaseURL, allowedIssuers, awsIdentityStoreID, awsIdentityStoreRegion)
			ctx.TraceCommand("", "eapi", commandArgs...)
			if ctx.DryRun {
				return nil
			}

			return launchAPI(ctx.Stdin, ctx.Stdout, ctx.Stderr, commandArgs)
		},
	}

	shared.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&host, "host", defaultAPIHost, "Host interface to bind the backend API HTTP server to")
	cmd.Flags().IntVar(&port, "port", config.APIServicePort, "Port to bind the backend API HTTP server to")
	cmd.Flags().StringVar(&databaseURL, "database-url", "", "Backend PostgreSQL database URL; defaults to ERUN_DATABASE_URL")
	cmd.Flags().StringVar(&allowedIssuers, "oidc-allowed-issuers", "", "Comma-separated OIDC issuer allow-list")
	cmd.Flags().StringVar(&awsIdentityStoreID, "aws-identity-store-id", "", "AWS IAM Identity Center identity store ID used to resolve usernames from STS tokens")
	cmd.Flags().StringVar(&awsIdentityStoreRegion, "aws-identity-store-region", "", "AWS region for Identity Store username lookup")
	cmd.Example = fmt.Sprintf("  erun api --host %s --port %d\n  erun api tenant-a dev", defaultAPIHost, config.APIServicePort)
	return cmd
}

func apiCommandArgs(host string, port int, databaseURL string, allowedIssuers string, awsIdentityStoreID string, awsIdentityStoreRegion string) []string {
	args := []string{
		"--host", host,
		"--port", strconv.Itoa(port),
	}
	if databaseURL != "" {
		args = append(args, "--database-url", databaseURL)
	}
	if allowedIssuers != "" {
		args = append(args, "--oidc-allowed-issuers", allowedIssuers)
	}
	if awsIdentityStoreID != "" {
		args = append(args, "--aws-identity-store-id", awsIdentityStoreID)
	}
	if awsIdentityStoreRegion != "" {
		args = append(args, "--aws-identity-store-region", awsIdentityStoreRegion)
	}
	return args
}

func LaunchAPIProcess(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	cmd := core.Command(resolveAPIExecutable(), args...)
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
