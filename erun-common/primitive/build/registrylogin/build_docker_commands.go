package registrylogin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
)

func DockerRegistryLogin(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
	if registrypkg.IsGHCRRegistry(registry) {
		ok, err := tryGHCRLoginViaGH(registry, stdout, stderr)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}

	args := []string{"login"}
	if registry != "" {
		args = append(args, registry)
	}

	loginCmd := core.Command("docker", args...)
	loginCmd.Stdin = stdin
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func isHostedRegistry(registry string) bool {
	return strings.EqualFold(strings.TrimSpace(registry), config.HostedRegistryHost)
}

// DockerRegistryLoginWithHostedRegistry logs in to the hosted registry with the operator's
// erun-api bearer token, deferring to DockerRegistryLogin otherwise.
func DockerRegistryLoginWithHostedRegistry(store config.CloudReadStore, deps cloud.CloudDependencies) seam.DockerRegistryLoginFunc {
	return func(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
		if isHostedRegistry(registry) {
			return hostedRegistryDockerLogin(store, deps, stdout, stderr)
		}
		return DockerRegistryLogin(registry, stdin, stdout, stderr)
	}
}

// hostedRegistryDockerLogin passes the token over stdin, never argv.
func hostedRegistryDockerLogin(store config.CloudReadStore, deps cloud.CloudDependencies, stdout, stderr io.Writer) error {
	provider, err := cloudconfig.ResolveERunPlatformAlias(store, "")
	if err != nil {
		return fmt.Errorf("erun's hosted registry authenticates with the tenant's own erun-api bearer token, minted from a configured erun cloud provider alias: %w", err)
	}
	token, err := cloud.CloudProviderBearerToken(core.Context{}, store, model.CloudBearerParams{Alias: provider.Alias}, deps)
	if err != nil {
		return fmt.Errorf("mint erun-api bearer token for hosted registry login: %w", err)
	}
	if strings.TrimSpace(token.Token) == "" {
		return fmt.Errorf("erun cloud provider alias %q returned an empty bearer token", provider.Alias)
	}
	loginCmd := core.Command("docker", "login", config.HostedRegistryHost, "-u", config.HostedRegistryLoginUsername, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token.Token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func tryGHCRLoginViaGH(registry string, stdout, stderr io.Writer) (bool, error) {
	if !dockerregistry.GHCLIAvailable() {
		return false, nil
	}

	user, ok := dockerregistry.CaptureGHValue("api", "user", "--jq", ".login")
	if !ok {
		return false, nil
	}

	token, ok := dockerregistry.CaptureGHValue("auth", "token")
	if !ok {
		return false, nil
	}

	loginCmd := core.Command("docker", "login", "ghcr.io", "-u", user, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

// RefreshGHCRPackageScopes re-mints the gh token with write:packages; use it only after
// TryGHCRNamespaceLogin and a retry still fail scope-denied. It refuses non-interactive contexts.
func RefreshGHCRPackageScopes(tag string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !registrypkg.IsGHCRRegistry(registrypkg.DockerRegistryFromImageTag(tag)) {
		return false, nil
	}
	namespace := registrypkg.DockerNamespaceFromTag(tag)
	if namespace == "" {
		return false, nil
	}
	if !dockerregistry.GHCLIAvailable() {
		return false, nil
	}

	// gh's browser flow hangs forever without an operator, so bail before mutating gh state.
	if !interactiveGHAuthAllowed(stdin) {
		return false, newNonInteractiveGHCRScopeRefreshError(tag, namespace)
	}

	switchCmd := core.Command("gh", "auth", "switch", "-h", "github.com", "-u", namespace)
	switchCmd.Stdout = stdout
	switchCmd.Stderr = stderr
	switchErr := switchCmd.Run()

	var ghCmd *exec.Cmd
	if switchErr == nil {
		ghCmd = core.Command("gh", "auth", "refresh", "-h", "github.com", "-s", "write:packages,read:packages")
	} else {
		ghCmd = core.Command("gh", "auth", "login", "-h", "github.com", "-s", "write:packages,read:packages", "-w", "--git-protocol", "https")
	}
	ghCmd.Stdin = stdin
	ghCmd.Stdout = stdout
	ghCmd.Stderr = stderr
	if err := ghCmd.Run(); err != nil {
		return false, err
	}

	return dockerregistry.TryGHCRNamespaceLogin(tag, stdout, stderr)
}

// interactiveGHAuthAllowed is false in a runtime pod (PTY but no browser) and for a
// non-terminal stdin; ERUN_FORCE_TTY=1 is a test seam.
func interactiveGHAuthAllowed(stdin io.Reader) bool {
	if core.InInjectedRuntimePod() {
		return false
	}
	if os.Getenv("ERUN_FORCE_TTY") == "1" {
		return true
	}
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func newNonInteractiveGHCRScopeRefreshError(tag, namespace string) error {
	registry := registrypkg.DockerRegistryFromImageTag(tag)
	if registry == "" {
		registry = "ghcr.io"
	}
	reason := "stdin is not an interactive terminal"
	if core.InInjectedRuntimePod() {
		reason = "this runtime pod is headless and has no browser"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s rejected the push: the gh-stored token lacks the write:packages scope.\n\n", registry)
	fmt.Fprintf(&sb, "erun can widen the scope automatically, but that needs gh's interactive browser login, which cannot run here (%s).\n", reason)
	sb.WriteString("From a host shell signed in to the namespace owner's GitHub account, with a browser available, run:\n")
	fmt.Fprintf(&sb, "  gh auth refresh -h github.com -u %s -s write:packages,read:packages\n", namespace)
	fmt.Fprintf(&sb, "  gh auth token -u %s -h github.com | docker login %s -u %s --password-stdin\n", namespace, registry, namespace)
	sb.WriteString("then re-run erun push.")
	return errors.New(sb.String())
}
