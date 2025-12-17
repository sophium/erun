package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	config "github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

// NewInitCmd bootstraps an erun tenant for the current repository.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize erun configuration for this repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, ".")
		},
	}
}

func runInit(cmd *cobra.Command, startPath string) error {
	// Discover git root and propose tenant name.
	gitRoot, err := config.FindGitRoot(startPath)
	if err != nil {
		if errors.Is(err, config.ErrGitNotFound) {
			return fmt.Errorf("unable to locate .git directory: %w", err)
		}
		return err
	}

	proposedTenant := strings.TrimSpace(filepath.Base(gitRoot))
	if proposedTenant == "" || proposedTenant == string(filepath.Separator) || proposedTenant == "." {
		proposedTenant = "default_tenant"
	}

	// If tenant already exists, exit early.
	tenantCfg, err := config.LoadTenantConfig(proposedTenant)
	if err == nil {
		if tenantCfg.Root == "" {
			tenantCfg.Root = proposedTenant
		}
		cmd.Printf("Tenant %q already configured for repository at %s\n", tenantCfg.Root, gitRoot)
		return nil
	}
	if !errors.Is(err, config.ErrNotInitialized) {
		return err
	}

	tenantName, err := promptTenantName(cmd, proposedTenant)
	if err != nil {
		return err
	}
	if tenantName == "" {
		tenantName = proposedTenant
	}

	tenantCfg = config.TenantConfig{Root: tenantName}
	if err := config.SaveTenantConfig(tenantCfg); err != nil {
		return err
	}

	// Ensure default environment config exists for the tenant.
	envCfg, err := config.LoadEnvConfig(tenantName, "default_env")
	if errors.Is(err, config.ErrNotInitialized) {
		envCfg = config.EnvConfig{Name: "default_env"}
	} else if err != nil {
		return err
	}
	if envCfg.Name == "" {
		envCfg.Name = "default_env"
	}
	if err := config.SaveEnvConfig(tenantName, envCfg); err != nil {
		return err
	}

	// Update the root erun config to point at this tenant.
	rootCfg, err := config.LoadERunConfig()
	if err != nil && !errors.Is(err, config.ErrNotInitialized) {
		return err
	}
	rootCfg.Tenant = tenantName
	if err := config.SaveErunConfig(rootCfg); err != nil {
		return err
	}

	cmd.Printf("Initialized tenant %q for repository at %s\n", tenantName, gitRoot)
	return nil
}

func promptTenantName(cmd *cobra.Command, proposed string) (string, error) {
	fmt.Fprintf(cmd.OutOrStdout(), "Initialize new tenant for this repository.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Tenant name [%s]: ", proposed)
	reader := bufio.NewReader(cmd.InOrStdin())
	input, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(input), nil
}
