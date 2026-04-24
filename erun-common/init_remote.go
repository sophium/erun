package eruncommon

import (
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"
)

type remoteRepositoryState struct {
	Exists    bool
	PublicKey string
}

const remoteRepositoryAccessRetryInterval = 2 * time.Second

func (s bootstrapRunner) ensureRemoteRepository(params BootstrapInitParams, tenant, envName, kubernetesContext, projectRoot string) error {
	target := OpenResult{
		Tenant:      tenant,
		Environment: envName,
		TenantConfig: TenantConfig{
			Name:               tenant,
			ProjectRoot:        projectRoot,
			DefaultEnvironment: envName,
		},
		EnvConfig: EnvConfig{
			Name:              envName,
			RepoPath:          projectRoot,
			KubernetesContext: kubernetesContext,
			Remote:            true,
		},
		RepoPath: projectRoot,
		Title:    tenant + "-" + envName,
	}
	req := ShellLaunchParamsFromResult(target)

	if err := s.ensureRemoteRuntime(target, req, params.RuntimeVersion); err != nil {
		return err
	}

	state, err := s.remoteRepositoryState(req, projectRoot)
	if err != nil {
		return err
	}
	if state.Exists {
		return s.pullRemoteRepository(req, projectRoot)
	}

	repositoryURL, err := s.resolveRemoteRepositoryURL(params, tenant, envName)
	if err != nil {
		return err
	}
	if err := s.waitForRemoteKeyImport(params, tenant, envName, req, repositoryURL, state.PublicKey); err != nil {
		return err
	}
	return s.cloneRemoteRepository(req, projectRoot, repositoryURL)
}

func (s bootstrapRunner) ensureRemoteRuntime(target OpenResult, req ShellLaunchParams, runtimeVersion string) error {
	spec, err := resolveDefaultDevopsDeploySpec(target)
	if err != nil {
		return err
	}
	spec.Deploy.ReleaseName = RuntimeReleaseName(target.Tenant)
	spec.Deploy.Version = strings.TrimSpace(runtimeVersion)
	if err := RunDeploySpec(s.Context, spec, nil, nil, s.DeployHelmChart); err != nil {
		return err
	}

	s.Context.TraceCommand("", "kubectl", kubectlDeploymentWaitArgs(req)...)
	if s.Context.DryRun || s.WaitForRemoteRuntime == nil {
		return nil
	}
	return s.WaitForRemoteRuntime(req)
}

func (s bootstrapRunner) resolveRemoteRepositoryURL(params BootstrapInitParams, tenant, envName string) (string, error) {
	if params.RemoteRepositoryURL != "" {
		return params.RemoteRepositoryURL, nil
	}
	interaction := BootstrapInitInteraction{
		Type:  BootstrapInitInteractionRemoteRepository,
		Label: remoteRepositoryLabel(tenant, envName),
	}
	if s.PromptRemoteRepositoryURL == nil {
		return "", BootstrapInitInteractionError{Interaction: interaction}
	}
	repositoryURL, err := s.PromptRemoteRepositoryURL(interaction.Label)
	if err != nil {
		return "", err
	}
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return "", BootstrapInitInteractionError{Interaction: interaction}
	}
	return repositoryURL, nil
}

func (s bootstrapRunner) waitForRemoteKeyImport(params BootstrapInitParams, tenant, envName string, req ShellLaunchParams, repositoryURL, publicKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey != "" {
		s.Context.Info("Import this SSH public key into your git host before continuing:")
		s.Context.Info(publicKey)
	}
	if params.ConfirmRemoteKeyImport != nil {
		if !*params.ConfirmRemoteKeyImport {
			return fmt.Errorf("remote SSH key import cancelled")
		}
	} else if s.Confirm == nil {
		return BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:    BootstrapInitInteractionConfirmRemoteKey,
			Label:   remoteKeyImportLabel(tenant, envName),
			Details: publicKey,
		}}
	}

	s.Context.Info("Waiting for the SSH key to be deployed to the git host. Rechecking every 2 seconds. Press Ctrl+C to cancel.")
	for attempts := 0; ; attempts++ {
		if err := s.verifyRemoteRepositoryAccess(req, repositoryURL); err == nil {
			if attempts > 0 {
				s.Context.Info("Remote repository access confirmed.")
			}
			return nil
		}

		s.Context.Info("SSH key not active yet; retrying in 2 seconds...")
		if s.Sleep != nil {
			s.Sleep(remoteRepositoryAccessRetryInterval)
		}
	}
}

func (s bootstrapRunner) remoteRepositoryState(req ShellLaunchParams, projectRoot string) (remoteRepositoryState, error) {
	script := strings.Join([]string{
		"set -eu",
		"mkdir -p \"$HOME/.ssh\"",
		"chmod 700 \"$HOME/.ssh\"",
		"key=\"$HOME/.ssh/id_ed25519\"",
		"if [ ! -f \"$key\" ]; then ssh-keygen -t ed25519 -N '' -f \"$key\" >/dev/null 2>&1; fi",
		"chmod 600 \"$key\"",
		"chmod 644 \"$key.pub\"",
		fmt.Sprintf("mkdir -p %s", shellQuote(projectRoot)),
		fmt.Sprintf("if [ -d %s/.git ]; then printf 'repo_exists\\n'; else printf 'repo_missing\\n'; fi", shellQuote(projectRoot)),
		"printf '__ERUN_REMOTE_PUBLIC_KEY__\\n'",
		"cat \"$key.pub\"",
	}, "\n")

	output, err := s.runRemoteScript(req, "remote-repository-state", script)
	if err != nil {
		return remoteRepositoryState{}, err
	}
	if s.Context.DryRun {
		return remoteRepositoryState{PublicKey: "<remote-public-key>", Exists: false}, nil
	}

	lines := strings.Split(strings.TrimSpace(output.Stdout), "\n")
	if len(lines) == 0 {
		return remoteRepositoryState{}, fmt.Errorf("remote repository state command returned no output")
	}
	state := remoteRepositoryState{Exists: strings.TrimSpace(lines[0]) == "repo_exists"}
	for index, line := range lines {
		if strings.TrimSpace(line) != "__ERUN_REMOTE_PUBLIC_KEY__" {
			continue
		}
		state.PublicKey = strings.TrimSpace(strings.Join(lines[index+1:], "\n"))
		break
	}
	return state, nil
}

func (s bootstrapRunner) verifyRemoteRepositoryAccess(req ShellLaunchParams, repositoryURL string) error {
	script := strings.Join([]string{
		"set -eu",
		"ssh_command='ssh -i \"$HOME/.ssh/id_ed25519\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'",
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" ls-remote %s HEAD >/dev/null", shellQuote(repositoryURL)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-access", script)
	if err != nil {
		return fmt.Errorf("verify remote repository access: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) cloneRemoteRepository(req ShellLaunchParams, projectRoot, repositoryURL string) error {
	script := strings.Join([]string{
		"set -eu",
		"ssh_command='ssh -i \"$HOME/.ssh/id_ed25519\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'",
		fmt.Sprintf("mkdir -p %s", shellQuote(path.Dir(projectRoot))),
		fmt.Sprintf("mkdir -p %s", shellQuote(projectRoot)),
		fmt.Sprintf("if [ -n \"$(ls -A %s 2>/dev/null)\" ] && [ ! -d %s/.git ]; then echo 'remote worktree directory exists and is not empty' >&2; exit 1; fi", shellQuote(projectRoot), shellQuote(projectRoot)),
		fmt.Sprintf("git -c core.sshCommand=\"$ssh_command\" clone %s %s", shellQuote(repositoryURL), shellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-clone", script)
	if err != nil {
		return fmt.Errorf("clone remote repository: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) pullRemoteRepository(req ShellLaunchParams, projectRoot string) error {
	script := strings.Join([]string{
		"set -eu",
		"ssh_command='ssh -i \"$HOME/.ssh/id_ed25519\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'",
		fmt.Sprintf("git -C %s -c core.sshCommand=\"$ssh_command\" pull --ff-only", shellQuote(projectRoot)),
	}, "\n")
	output, err := s.runRemoteScript(req, "remote-repository-pull", script)
	if err != nil {
		return fmt.Errorf("pull remote repository: %w%s", err, formatRemoteCommandStderr(output.Stderr))
	}
	return nil
}

func (s bootstrapRunner) runRemoteScript(req ShellLaunchParams, label, script string) (RemoteCommandResult, error) {
	traceArgs := append([]string{}, kubectlRemoteExecArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	s.Context.TraceCommand("", "kubectl", traceArgs...)
	s.Context.TraceBlock(label, script)
	if s.Context.DryRun {
		return RemoteCommandResult{}, nil
	}
	return s.RunRemoteCommand(req, script)
}

func kubectlRemoteExecArgs(req ShellLaunchParams, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-c", RuntimeReleaseName(req.Tenant))
	args = append(args, "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

func RunRemoteCommand(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
	cmd := exec.Command("kubectl", kubectlRemoteExecArgs(req, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func formatRemoteCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}
