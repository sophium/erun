package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

const (
	initializeCurrentProjectOption       = "Initialize current project"
	enterKubernetesContextManuallyOption = "Enter Kubernetes context manually"
)

func newInitCmd(runInit func(common.Context, common.BootstrapInitParams) error) *cobra.Command {
	params := common.BootstrapInitParams{}
	setDefaultTenant := false
	confirmEnvironment := false

	cmd := &cobra.Command{
		Use:          "init [TENANT] [ENVIRONMENT]",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runParams := params
			if runParams.Tenant == "" && len(args) > 0 {
				runParams.Tenant = args[0]
			}
			if runParams.Environment == "" && len(args) > 1 {
				runParams.Environment = args[1]
			}
			if runParams.Remote && runParams.Tenant == "" {
				return fmt.Errorf("tenant is required with --remote")
			}
			if runParams.Remote && strings.TrimSpace(runParams.Environment) == "" {
				return fmt.Errorf("environment is required with --remote")
			}
			if cmd.Flags().Changed("set-default-tenant") {
				runParams.ConfirmTenant = &setDefaultTenant
			}
			if cmd.Flags().Changed("confirm-environment") {
				runParams.ConfirmEnvironment = &confirmEnvironment
			}
			return runInit(commandContext(cmd), runParams)
		},
	}

	cmd.Flags().StringVar(&params.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&params.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&params.Environment, "environment", "", "Environment name")
	cmd.Flags().StringVar(&params.RuntimeVersion, "version", "", "Runtime image version to initialize and deploy")
	cmd.Flags().StringVar(&params.RuntimeImage, "runtime-image", "", "Runtime image repository to initialize and deploy")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context to associate with the environment")
	cmd.Flags().StringVar(&params.ContainerRegistry, "container-registry", "", "Container registry to associate with the environment")
	cmd.Flags().StringVar(&params.CodeCommitSSHKeyID, "codecommit-ssh-key-id", "", "CodeCommit SSH public key ID to use for remote repository access")
	cmd.Flags().BoolVar(&params.Bootstrap, "bootstrap", false, "Create the tenant devops module and chart during initialization")
	cmd.Flags().BoolVar(&params.Remote, "remote", false, "Initialize the tenant repository inside the runtime pod instead of the local host")
	cmd.Flags().BoolVar(&params.NoGit, "no-git", false, "Skip remote Git checkout setup when used with --remote")
	cmd.Flags().BoolVar(&setDefaultTenant, "set-default-tenant", false, "Set the initialized tenant as the default tenant")
	cmd.Flags().BoolVar(&confirmEnvironment, "confirm-environment", false, "Confirm environment initialization without prompting")
	cmd.Flags().BoolVarP(&params.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	addDryRunFlag(cmd)
	return cmd
}

func containerRegistryPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label:   label,
		Default: common.DefaultContainerRegistry,
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", fmt.Errorf("container registry configuration interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", common.ErrContainerRegistryCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return common.DefaultContainerRegistry, nil
	}
	return result, nil
}

func remoteRepositoryURLPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("repository remote URL is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func codeCommitSSHKeyIDPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("CodeCommit SSH public key ID is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func confirmPrompt(run PromptRunner, label string) (bool, error) {
	label = strings.TrimRight(strings.TrimSpace(label), "?")
	prompt := promptui.Prompt{
		Label: label,
		Templates: &promptui.PromptTemplates{
			Prompt:  `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Valid:   `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Invalid: `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Success: `{{ . | faint }}? `,
		},
		Validate: func(input string) error {
			switch strings.ToLower(strings.TrimSpace(input)) {
			case "", "y", "n":
				return nil
			default:
				return fmt.Errorf("enter y or n")
			}
		},
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

	return strings.EqualFold(strings.TrimSpace(result), "y"), nil
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
			return "", common.ErrKubernetesContextCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", common.ErrKubernetesContextCancelled
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
			return "", false, common.ErrKubernetesContextCancelled
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

func listKubernetesContexts() ([]string, error) {
	output, err := exec.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}
	contexts := strings.Split(string(output), "\n")

	currentOutput, err := exec.Command("kubectl", "config", "current-context").Output()
	if err == nil {
		contexts = preferCurrentKubernetesContext(contexts, string(currentOutput))
	}

	return contexts, nil
}

func ensureKubernetesNamespace(contextName, namespace string) error {
	return common.EnsureKubernetesNamespace(contextName, namespace)
}

func preferCurrentKubernetesContext(contexts []string, current string) []string {
	current = strings.TrimSpace(current)
	if current == "" {
		return contexts
	}

	result := make([]string, 0, len(contexts))
	result = append(result, current)
	for _, context := range contexts {
		if strings.TrimSpace(context) == current {
			continue
		}
		result = append(result, context)
	}
	return result
}

func selectTenantPrompt(run SelectRunner, tenants []common.TenantConfig) (common.TenantSelectionResult, error) {
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
			return common.TenantSelectionResult{}, fmt.Errorf("tenant selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return common.TenantSelectionResult{}, nil
		}
		return common.TenantSelectionResult{}, err
	}

	if result == initializeCurrentProjectOption {
		return common.TenantSelectionResult{Initialize: true}, nil
	}

	return common.TenantSelectionResult{Tenant: result}, nil
}
