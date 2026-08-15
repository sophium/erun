package cmd

import (
	"errors"
	"fmt"
	"io"
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
	clusterRegistry := false
	var envType string

	cmd := &cobra.Command{
		Use:          "init [TENANT] [ENVIRONMENT]",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runParams, err := initRunParams(cmd, args, params, setDefaultTenant, confirmEnvironment, clusterRegistry, envType)
			if err != nil {
				return err
			}
			return runInit(commandContext(cmd), runParams)
		},
	}

	cmd.Flags().StringVar(&params.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&params.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&params.Environment, "environment", "", "Environment name")
	cmd.Flags().StringVar(&params.RuntimeVersion, "version", "", "Runtime image version to initialize and deploy")
	cmd.Flags().StringVar(&params.RuntimeImage, "runtime-image", "", "Runtime image repository to initialize and deploy")
	cmd.Flags().StringSliceVar(&params.ImagePullSecrets, "image-pull-secret", nil, "Kubernetes dockerconfigjson secret the runtime pod pulls its image with; repeat or comma-separate for several. Required when the runtime image is in a private registry")
	cmd.Flags().StringVar(&params.RuntimePod.CPU, "runtime-cpu", "", "Runtime pod CPU limit")
	cmd.Flags().StringVar(&params.RuntimePod.Memory, "runtime-memory", "", "Runtime pod memory limit")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context to associate with the environment")
	cmd.Flags().StringVar(&params.ContainerRegistry, "container-registry", "", "Container registry to associate with the environment")
	cmd.Flags().BoolVar(&clusterRegistry, "cluster-registry", false, "Use the in-cluster erun-registry (addresses resolved from the env's kube-context) instead of --container-registry; mutually exclusive with it")
	cmd.Flags().StringVar(&params.CodeCommitSSHKeyID, "codecommit-ssh-key-id", "", "CodeCommit SSH public key ID to use for remote repository access")
	cmd.Flags().BoolVar(&params.Bootstrap, "bootstrap", false, "Deprecated: ignored; remote runtimes deploy the published erun-devops chart")
	_ = cmd.Flags().MarkDeprecated("bootstrap", "remote runtimes deploy the published erun-devops chart; the flag is ignored")
	cmd.Flags().StringVar(&envType, "type", "", "Environment type: local-agent, remote-agent, or runtime (takes precedence over --remote)")
	cmd.Flags().BoolVar(&params.Remote, "remote", false, "Deprecated alias for --type=remote-agent")
	cmd.Flags().BoolVar(&params.NoGit, "no-git", false, "Skip remote Git checkout setup when used with --remote or --type=remote-agent")
	cmd.Flags().BoolVar(&setDefaultTenant, "set-default-tenant", false, "Set the initialized tenant as the default tenant")
	cmd.Flags().BoolVar(&confirmEnvironment, "confirm-environment", false, "Confirm environment initialization without prompting")
	cmd.Flags().BoolVarP(&params.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	cmd.Flags().BoolVar(&params.DisableBuildScript, "disable-build-script", false, "Ignore any project build.sh for this env; erun build resolves docker/release contexts directly")
	cmd.Flags().BoolVar(&params.PlatformAccount, "platform-account", false, "Make this env a cluster platform account: deploy binds its runtime ServiceAccount to cluster-admin so in-pod platform terraform (cluster edge) and component installs can manage cluster-scoped resources")
	cmd.Flags().StringVar(&params.MCPAuthPublicKeyPath, "mcp-auth-public-key", "", "Require the env's MCP edge to authenticate bearer tokens signed by this PEM public key; empty leaves the edge loopback-only. Folds MCP auth into init's single runtime deploy.")
	addDryRunFlag(cmd)
	return cmd
}

func initRunParams(cmd *cobra.Command, args []string, params common.BootstrapInitParams, setDefaultTenant, confirmEnvironment, clusterRegistry bool, envType string) (common.BootstrapInitParams, error) {
	runParams := params
	applyPositionalInitArgs(&runParams, args)
	if err := applyClusterRegistryFlag(&runParams, clusterRegistry); err != nil {
		return common.BootstrapInitParams{}, err
	}
	if err := applyInitTypeFlag(cmd, &runParams, envType); err != nil {
		return common.BootstrapInitParams{}, err
	}
	if err := validateInitRunParams(runParams); err != nil {
		return common.BootstrapInitParams{}, err
	}
	if cmd.Flags().Changed("set-default-tenant") {
		runParams.ConfirmTenant = &setDefaultTenant
	}
	if cmd.Flags().Changed("confirm-environment") {
		runParams.ConfirmEnvironment = &confirmEnvironment
	}
	return runParams, nil
}

func applyPositionalInitArgs(runParams *common.BootstrapInitParams, args []string) {
	if runParams.Tenant == "" && len(args) > 0 {
		runParams.Tenant = args[0]
	}
	if runParams.Environment == "" && len(args) > 1 {
		runParams.Environment = args[1]
	}
}

func applyClusterRegistryFlag(runParams *common.BootstrapInitParams, clusterRegistry bool) error {
	if !clusterRegistry {
		return nil
	}
	if strings.TrimSpace(runParams.ContainerRegistry) != "" {
		return fmt.Errorf("--cluster-registry conflicts with --container-registry; pick one")
	}
	// The in-cluster erun-registry is plain HTTP, so mark it insecure; the
	// rest (service/namespace/port) fill from the erun-registry convention.
	runParams.ClusterRegistry = &common.ClusterRegistry{Insecure: true}
	return nil
}

func applyInitTypeFlag(cmd *cobra.Command, runParams *common.BootstrapInitParams, envType string) error {
	envType = strings.TrimSpace(envType)
	if envType == "" {
		return nil
	}
	parsed := common.EnvironmentType(envType)
	if !parsed.IsValid() {
		return fmt.Errorf("invalid --type %q: must be local-agent, remote-agent, or runtime", envType)
	}
	remoteFlag := cmd.Flags().Lookup("remote")
	remoteChanged := remoteFlag != nil && remoteFlag.Changed
	expectedRemote := parsed != common.EnvironmentTypeLocalAgent
	if remoteChanged && runParams.Remote != expectedRemote {
		return fmt.Errorf("--type=%s conflicts with --remote=%t", envType, runParams.Remote)
	}
	runParams.Type = parsed
	runParams.Remote = expectedRemote
	return nil
}

func validateInitRunParams(params common.BootstrapInitParams) error {
	if params.RemoteWorktree() && params.Tenant == "" {
		return fmt.Errorf("tenant is required for non-local-agent envs")
	}
	if params.RemoteWorktree() && strings.TrimSpace(params.Environment) == "" {
		return fmt.Errorf("environment is required for non-local-agent envs")
	}
	return nil
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
	return confirmPromptTo(run, label, nil)
}

// confirmPromptTo renders the confirm to out instead of promptui's default
// (os.Stdout) when out is non-nil. The open --no-shell alias flow routes it to
// stderr: promptui repaints the confirm from a readline goroutine, and it prints
// the eval-able setup script to stdout immediately afterward, so leaving the
// prompt on stdout lets the two writers interleave non-deterministically (on
// Windows the repaint frames fused into the first script line ~1 run in 20).
// Keeping the UI on stderr leaves stdout carrying only the script.
func confirmPromptTo(run PromptRunner, label string, out io.WriteCloser) (bool, error) {
	label = strings.TrimRight(strings.TrimSpace(label), "?")
	prompt := promptui.Prompt{
		Label:  label,
		Stdout: out,
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
	output, err := common.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}
	contexts := strings.Split(string(output), "\n")

	currentOutput, err := common.Command("kubectl", "config", "current-context").Output()
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
