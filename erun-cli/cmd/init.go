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

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runParams := params
			if runParams.Tenant == "" && len(args) > 0 {
				runParams.Tenant = args[0]
			}
			return runInit(commandContext(cmd), runParams)
		},
	}

	cmd.Flags().StringVar(&params.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&params.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&params.Environment, "environment", "", "Environment name")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context to associate with the environment")
	cmd.Flags().StringVar(&params.ContainerRegistry, "container-registry", "", "Container registry to associate with the environment")
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
	if exists, err := kubernetesNamespaceExists(contextName, namespace); err != nil {
		return err
	} else if exists {
		return nil
	}

	output, err := exec.Command("kubectl", "--context", contextName, "create", "namespace", namespace).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if kubernetesNamespaceAlreadyExists(message) {
			return nil
		}
		if message == "" {
			return fmt.Errorf("failed to ensure kubernetes namespace %q in context %q: %w", namespace, contextName, err)
		}
		return fmt.Errorf("failed to ensure kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
	}

	return nil
}

func kubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	output, err := exec.Command("kubectl", "--context", contextName, "get", "namespace", namespace, "-o", "name").CombinedOutput()
	if err == nil {
		return true, nil
	}

	message := strings.TrimSpace(string(output))
	if kubernetesNamespaceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

func kubernetesNamespaceNotFound(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func kubernetesNamespaceAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "alreadyexists") || strings.Contains(message, "already exists")
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
