package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/manifoldco/promptui"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	initializeCurrentProjectOption       = "Initialize current project"
	enterKubernetesContextManuallyOption = "Enter Kubernetes context manually"
)

func NewInitCmd(deps Dependencies, verbosity *int) *cobra.Command {
	req := bootstrap.InitRequest{}

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			request := req
			if request.Tenant == "" && len(args) > 0 {
				request.Tenant = args[0]
			}
			return runInitCommand(cmd, deps, verbosity, request)
		},
	}

	cmd.Flags().StringVar(&req.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&req.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&req.Environment, "environment", "", "Default environment name")
	cmd.Flags().StringVar(&req.KubernetesContext, "kubernetes-context", "", "Kubernetes context to associate with the environment")
	cmd.Flags().StringVar(&req.ContainerRegistry, "container-registry", "", "Container registry to associate with the environment")
	cmd.Flags().BoolVarP(&req.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	addDryRunFlag(cmd)
	return cmd
}

func runInitCommand(cmd *cobra.Command, deps Dependencies, verbosity *int, req bootstrap.InitRequest) error {
	deps = withDependencyDefaults(deps)
	var dryRunRecorder *initDryRunRecorder
	projectConfigSaver := internal.SaveProjectConfig
	if isDryRunCommand(cmd) {
		dryRunRecorder = &initDryRunRecorder{}
		deps = dryRunInitDependencies(deps, dryRunRecorder)
		emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: dry-run suppresses configuration writes and namespace creation")
		projectConfigSaver = dryRunRecorder.SaveProjectConfig
	}

	logger := eruncommon.NewLoggerWithWriters(internalTraceVerbosity(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
	service := bootstrap.Service{
		Store:           deps.Store,
		FindProjectRoot: deps.FindProjectRoot,
		SelectTenant: func(tenants []internal.TenantConfig) (bootstrap.TenantSelectionResult, error) {
			return selectTenantPrompt(deps.SelectRunner, tenants)
		},
		Confirm: func(label string) (bool, error) {
			return confirmPrompt(deps.PromptRunner, label)
		},
		PromptKubernetesContext: func(label string) (string, error) {
			return kubernetesContextPrompt(deps.PromptRunner, deps.SelectRunner, deps.ListKubernetesContexts, label)
		},
		PromptContainerRegistry: func(label string) (string, error) {
			return containerRegistryPrompt(deps.PromptRunner, label)
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if deps.EnsureKubernetesNamespace == nil {
				return nil
			}
			return deps.EnsureKubernetesNamespace(contextName, namespace)
		},
		LoadProjectConfig: internal.LoadProjectConfig,
		SaveProjectConfig: projectConfigSaver,
		Logger:            logger,
	}

	result, err := service.Run(req)
	if err != nil {
		return err
	}
	if dryRunRecorder != nil {
		emitInitDryRunActions(cmd, dryRunRecorder)
	}
	emitTraceNotes(cmd, cmd.ErrOrStderr(), initResultDecisionNotes(result)...)
	return nil
}

func initResultDecisionNotes(result bootstrap.InitResult) []string {
	notes := make([]string, 0, 8)
	if tenant := strings.TrimSpace(result.TenantConfig.Name); tenant != "" {
		notes = append(notes, "decision: resolved tenant="+tenant)
	}
	if projectRoot := strings.TrimSpace(result.TenantConfig.ProjectRoot); projectRoot != "" {
		notes = append(notes, "decision: resolved project root="+projectRoot)
	}
	if environment := strings.TrimSpace(result.EnvConfig.Name); environment != "" {
		notes = append(notes, "decision: resolved environment="+environment)
		namespace := bootstrap.KubernetesNamespaceName(strings.TrimSpace(result.TenantConfig.Name), environment)
		if namespace != "" {
			notes = append(notes, "decision: resolved namespace="+namespace)
		}
	}
	if contextName := strings.TrimSpace(result.EnvConfig.KubernetesContext); contextName != "" {
		notes = append(notes, "decision: resolved kubernetes context="+contextName)
	}
	if registry := strings.TrimSpace(result.EnvConfig.ContainerRegistry); registry != "" {
		notes = append(notes, "decision: resolved container registry="+registry)
	}
	if result.CreatedERunConfig {
		notes = append(notes, "decision: the default erun configuration was missing and will be created")
	}
	if result.CreatedTenantConfig {
		notes = append(notes, "decision: the tenant configuration was missing and will be created")
	}
	if result.CreatedEnvConfig {
		notes = append(notes, "decision: the environment configuration was missing and will be created")
	}
	return notes
}

func dryRunInitDependencies(deps Dependencies, recorder *initDryRunRecorder) Dependencies {
	deps.Store = dryRunBootstrapStore{Store: deps.Store, recorder: recorder}
	deps.EnsureKubernetesNamespace = recorder.EnsureKubernetesNamespace
	return deps
}

type dryRunBootstrapStore struct {
	bootstrap.Store
	recorder *initDryRunRecorder
}

func (s dryRunBootstrapStore) SaveERunConfig(config internal.ERunConfig) error {
	return s.recorder.SaveERunConfig(config)
}

func (s dryRunBootstrapStore) SaveTenantConfig(config internal.TenantConfig) error {
	return s.recorder.SaveTenantConfig(config)
}

func (s dryRunBootstrapStore) SaveEnvConfig(tenant string, config internal.EnvConfig) error {
	return s.recorder.SaveEnvConfig(tenant, config)
}

type initDryRunRecorder struct {
	writes    []CommandTrace
	namespace []CommandTrace
}

func (r *initDryRunRecorder) SaveERunConfig(config internal.ERunConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", "config.yaml"))
	if err != nil {
		return internal.ErrNoUserDataFolder
	}
	return r.recordYAMLWrite(configPath, config)
}

func (r *initDryRunRecorder) SaveTenantConfig(config internal.TenantConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", config.Name, "config.yaml"))
	if err != nil {
		return internal.ErrNoUserDataFolder
	}
	return r.recordYAMLWrite(configPath, config)
}

func (r *initDryRunRecorder) SaveEnvConfig(tenant string, config internal.EnvConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", tenant, config.Name, "config.yaml"))
	if err != nil {
		return internal.ErrNoUserDataFolder
	}
	return r.recordYAMLWrite(configPath, config)
}

func (r *initDryRunRecorder) SaveProjectConfig(projectRoot string, config internal.ProjectConfig) error {
	if strings.TrimSpace(projectRoot) == "" {
		return internal.ErrNotInGitRepository
	}
	configPath := filepath.Join(filepath.Clean(projectRoot), ".erun", "config.yaml")
	return r.recordYAMLWrite(configPath, config)
}

func (r *initDryRunRecorder) EnsureKubernetesNamespace(contextName, namespace string) error {
	r.namespace = append(r.namespace,
		CommandTrace{
			Name: "kubectl",
			Args: []string{"create", "namespace", namespace, "--dry-run=client", "-o", "yaml"},
		},
		CommandTrace{
			Name: "kubectl",
			Args: []string{"--context", contextName, "apply", "-f", "-"},
		},
	)
	return nil
}

func (r *initDryRunRecorder) recordYAMLWrite(path string, value any) error {
	if _, err := yaml.Marshal(value); err != nil {
		return internal.ErrFailedToSaveConfig
	}
	r.writes = append(r.writes,
		CommandTrace{
			Name: "mkdir",
			Args: []string{"-p", filepath.Dir(path)},
		},
		CommandTrace{
			Name: "write-yaml",
			Args: []string{path},
		},
	)
	return nil
}

func emitInitDryRunActions(cmd *cobra.Command, recorder *initDryRunRecorder) {
	if recorder == nil {
		return
	}
	for _, trace := range recorder.namespace {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), trace)
	}
	for _, trace := range recorder.writes {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), trace)
	}
}

func containerRegistryPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label:   label,
		Default: bootstrap.DefaultContainerRegistry,
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", fmt.Errorf("container registry configuration interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", bootstrap.ErrContainerRegistryCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return bootstrap.DefaultContainerRegistry, nil
	}
	return result, nil
}

func confirmPrompt(run PromptRunner, label string) (bool, error) {
	prompt := promptui.Prompt{
		Label:     label,
		IsConfirm: true,
		Default:   "y",
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("initialization interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}

	if result == "" {
		return true, nil
	}

	return strings.EqualFold(result, "y"), nil
}

func kubernetesContextPrompt(run PromptRunner, selectRun SelectRunner, list KubernetesContextsLister, label string) (string, error) {
	if list != nil {
		contexts, err := list()
		if err == nil {
			contexts = normalizeKubernetesContexts(contexts)
			if len(contexts) > 0 && selectRun != nil {
				selected, manual, err := selectKubernetesContextPrompt(selectRun, label, contexts)
				if err != nil {
					return "", err
				}
				if !manual {
					return selected, nil
				}
			}
		}
	}

	return manualKubernetesContextPrompt(run, label)
}

func manualKubernetesContextPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("kubernetes context is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", fmt.Errorf("kubernetes context association interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", bootstrap.ErrKubernetesContextCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", bootstrap.ErrKubernetesContextCancelled
	}
	return result, nil
}

func selectKubernetesContextPrompt(run SelectRunner, label string, contexts []string) (string, bool, error) {
	items := append(append([]string{}, contexts...), enterKubernetesContextManuallyOption)

	prompt := promptui.Select{
		Label: label,
		Items: items,
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", false, fmt.Errorf("kubernetes context selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", false, bootstrap.ErrKubernetesContextCancelled
		}
		return "", false, err
	}

	if result == enterKubernetesContextManuallyOption {
		return "", true, nil
	}
	return strings.TrimSpace(result), false, nil
}

func normalizeKubernetesContexts(contexts []string) []string {
	seen := make(map[string]struct{}, len(contexts))
	result := make([]string, 0, len(contexts))
	for _, context := range contexts {
		context = strings.TrimSpace(context)
		if context == "" {
			continue
		}
		if _, ok := seen[context]; ok {
			continue
		}
		seen[context] = struct{}{}
		result = append(result, context)
	}
	return result
}

func defaultKubernetesContextsLister() ([]string, error) {
	output, err := exec.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(string(output), "\n"), nil
}

func defaultKubernetesNamespaceEnsurer(contextName, namespace string) error {
	manifest, err := exec.Command("kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml").Output()
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "--context", contextName, "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(manifest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, message)
	}

	return nil
}

func selectTenantPrompt(run SelectRunner, tenants []internal.TenantConfig) (bootstrap.TenantSelectionResult, error) {
	items := make([]string, 0, len(tenants)+1)
	for _, tenant := range tenants {
		items = append(items, tenant.Name)
	}
	items = append(items, initializeCurrentProjectOption)

	prompt := promptui.Select{
		Label: "Select tenant",
		Items: items,
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return bootstrap.TenantSelectionResult{}, fmt.Errorf("tenant selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return bootstrap.TenantSelectionResult{}, nil
		}
		return bootstrap.TenantSelectionResult{}, err
	}

	if result == initializeCurrentProjectOption {
		return bootstrap.TenantSelectionResult{Initialize: true}, nil
	}

	return bootstrap.TenantSelectionResult{Tenant: result}, nil
}
