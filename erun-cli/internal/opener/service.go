package opener

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/internal"
)

var (
	ErrDefaultTenantNotConfigured      = errors.New("default tenant is not configured")
	ErrDefaultEnvironmentNotConfigured = errors.New("default environment is not configured")
	ErrTenantNotFound                  = errors.New("no such tenant exists")
	ErrEnvironmentNotFound             = errors.New("no such environment exists")
	ErrRepoPathNotConfigured           = errors.New("repo path is not configured")
)

type Store interface {
	LoadERunConfig() (internal.ERunConfig, string, error)
	LoadTenantConfig(string) (internal.TenantConfig, string, error)
	LoadEnvConfig(string, string) (internal.EnvConfig, string, error)
}

type Request struct {
	Tenant                string
	Environment           string
	UseDefaultTenant      bool
	UseDefaultEnvironment bool
}

type Result struct {
	Tenant       string
	Environment  string
	TenantConfig internal.TenantConfig
	EnvConfig    internal.EnvConfig
	RepoPath     string
	Title        string
}

type ShellLaunchRequest struct {
	Dir   string
	Title string
}

type ShellLauncher func(ShellLaunchRequest) error

type Service struct {
	Store       Store
	LaunchShell ShellLauncher
}

func (s Service) Resolve(req Request) (Result, error) {
	if s.Store == nil {
		return Result{}, fmt.Errorf("store is required")
	}

	tenant := req.Tenant
	if tenant == "" && req.UseDefaultTenant {
		toolConfig, _, err := s.Store.LoadERunConfig()
		if errors.Is(err, internal.ErrNotInitialized) {
			return Result{}, ErrDefaultTenantNotConfigured
		}
		if err != nil {
			return Result{}, err
		}
		tenant = toolConfig.DefaultTenant
		if tenant == "" {
			return Result{}, ErrDefaultTenantNotConfigured
		}
	}
	if tenant == "" {
		return Result{}, fmt.Errorf("tenant is required")
	}

	tenantConfig, _, err := s.Store.LoadTenantConfig(tenant)
	if errors.Is(err, internal.ErrNotInitialized) {
		return Result{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return Result{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}

	environment := req.Environment
	if environment == "" && req.UseDefaultEnvironment {
		environment = tenantConfig.DefaultEnvironment
		if environment == "" {
			return Result{}, ErrDefaultEnvironmentNotConfigured
		}
	}
	if environment == "" {
		return Result{}, fmt.Errorf("environment is required")
	}

	envConfig, _, err := s.Store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, internal.ErrNotInitialized) {
		return Result{}, fmt.Errorf("%w: %s", ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return Result{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}

	repoPath := envConfig.RepoPath
	if repoPath == "" {
		repoPath = tenantConfig.ProjectRoot
	}
	if repoPath == "" {
		return Result{}, ErrRepoPathNotConfigured
	}

	repoPath = filepath.Clean(repoPath)
	info, err := os.Stat(repoPath)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%q is not a directory", repoPath)
	}

	return Result{
		Tenant:       tenant,
		Environment:  environment,
		TenantConfig: tenantConfig,
		EnvConfig:    envConfig,
		RepoPath:     repoPath,
		Title:        tenant + "-" + environment,
	}, nil
}

func (s Service) Run(req Request) (Result, error) {
	result, err := s.Resolve(req)
	if err != nil {
		return Result{}, err
	}

	launcher := s.LaunchShell
	if launcher == nil {
		launcher = DefaultShellLauncher
	}

	if err := launcher(ShellLaunchRequest{
		Dir:   result.RepoPath,
		Title: result.Title,
	}); err != nil {
		return Result{}, err
	}

	return result, nil
}

func DefaultShellLauncher(req ShellLaunchRequest) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	script := fmt.Sprintf(
		"printf '\\033]0;%%s\\007' %s; exec %s -i",
		shellQuote(req.Title),
		shellQuote(shell),
	)

	cmd := exec.Command("/bin/sh", "-lc", script)
	cmd.Dir = req.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
