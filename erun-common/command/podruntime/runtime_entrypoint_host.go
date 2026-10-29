package podruntime

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

func (b runtimeBoot) kubeconfigPath() string {
	return b.getOr("KUBECONFIG", b.path(".kube", "config"))
}

func describeRuntimeKubeconfig(boot runtimeBoot) string {
	if boot.get("KUBERNETES_SERVICE_HOST") == "" {
		return ""
	}
	return "write " + boot.kubeconfigPath() + " for the pod's service account"
}

// writeRuntimeKubeconfig also adds the outer cloud-context name as a context on the same
// cluster, since synced configs run `kubectl --context <outer-name>` inside the pod.
func writeRuntimeKubeconfig(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	tokenFile := filepath.Join(boot.serviceAccountDir, "token")
	caFile := filepath.Join(boot.serviceAccountDir, "ca.crt")
	namespaceFile := filepath.Join(boot.serviceAccountDir, "namespace")
	for _, required := range []string{tokenFile, caFile, namespaceFile} {
		if !pathExists(required) {
			return nil // no mounted service account: nothing to point at
		}
	}
	namespace, ok := readFileIfReadable(namespaceFile)
	if !ok {
		return nil
	}
	server := "https://" + boot.get("KUBERNETES_SERVICE_HOST") + ":" + boot.getOr("KUBERNETES_SERVICE_PORT_HTTPS", "443")
	contexts := []string{providerconfig.InClusterKubernetesContext}
	outer := boot.getOr("ERUN_CLOUD_CONTEXT_NAME", boot.get("ERUN_KUBERNETES_CONTEXT"))
	if outer != "" && outer != providerconfig.InClusterKubernetesContext {
		contexts = append(contexts, outer)
	}
	var config strings.Builder
	fmt.Fprintf(&config, "apiVersion: v1\nkind: Config\nclusters:\n  - cluster:\n      certificate-authority: %s\n      server: %s\n    name: in-cluster\ncontexts:\n", caFile, server)
	for _, name := range contexts {
		fmt.Fprintf(&config, "  - context:\n      cluster: in-cluster\n      namespace: %s\n      user: erun-devops\n    name: %s\n", strings.TrimSpace(string(namespace)), name)
	}
	fmt.Fprintf(&config, "current-context: in-cluster\nusers:\n  - name: erun-devops\n    user:\n      tokenFile: %s\n", tokenFile)

	path := boot.kubeconfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(config.String()), 0o600)
}

func describeRuntimeSSHPermissions(boot runtimeBoot) string {
	return "restore private modes under " + boot.path(".ssh")
}

// Environments that once ran with a fsGroup still carry g+rw on the home volume, and ssh
// refuses a private key looser than 0600.
func normalizeRuntimeSSHPermissions(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	dir := boot.path(".ssh")
	if entries, err := os.ReadDir(dir); err == nil {
		restrictRuntimeSSHModes(dir, entries)
	}
	return nil
}

func restrictRuntimeSSHModes(dir string, entries []os.DirEntry) {
	_ = os.Chmod(dir, 0o700)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(entry.Name(), ".pub") {
			mode = 0o644
		}
		_ = os.Chmod(filepath.Join(dir, entry.Name()), mode)
	}
}

func describeRuntimeOutputsDir(boot runtimeBoot) string {
	return "create " + boot.outputsDir()
}

// The image creates the default outputs dir; an overridden path may not exist yet.
func ensureRuntimeOutputsDir(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	return os.MkdirAll(boot.outputsDir(), 0o755)
}

func describeRuntimeGitSafeDirectory(boot runtimeBoot) string {
	return "mark " + boot.repoDir() + " as a git safe.directory"
}

// On Windows local-agent environments the worktree surfaces as root:root, tripping git's
// dubious-owner guard. Children run through the supervisor so the init does not reap
// them before os/exec waits.
func ensureRuntimeGitSafeDirectory(_ core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	if !onPath("git") {
		return nil
	}
	repoDir := boot.repoDir()
	var existing bytes.Buffer
	read := core.Command("git", "config", "--global", "--get-all", "safe.directory")
	read.Stdout = &existing
	_ = supervisor.run(read) // exits 1 when no safe.directory is set yet
	for _, line := range strings.Split(existing.String(), "\n") {
		if strings.TrimSpace(line) == repoDir {
			return nil
		}
	}
	return supervisor.run(core.Command("git", "config", "--global", "--add", "safe.directory", repoDir))
}

func describeRuntimeSource(boot runtimeBoot) string {
	url := boot.get("ERUN_REPO_URL")
	if url == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(boot.repoDir(), ".git")); err == nil {
		return ""
	}
	description := "clone " + url + " into " + boot.repoDir()
	if ref := boot.get("ERUN_REPO_REF"); ref != "" {
		description += " and check out " + ref
	}
	return description
}

// ensureRuntimeSource clones only into an empty worktree so live patches survive restarts.
func ensureRuntimeSource(ctx core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	url := boot.get("ERUN_REPO_URL")
	repoDir := boot.repoDir()
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return err
	}
	if err := supervisor.run(core.Command("git", "clone", url, repoDir)); err == nil {
		checkOutRuntimeSourceRef(ctx, boot.get("ERUN_REPO_REF"), repoDir, supervisor)
	} else {
		ctx.Info("erun: failed to clone runtime source from " + url + "; continuing without mounted source")
	}
	return nil
}

func checkOutRuntimeSourceRef(ctx core.Context, ref, repoDir string, supervisor *runtimeSupervisor) {
	if ref == "" {
		return
	}
	checkout := core.Command("git", "checkout", "-q", ref)
	checkout.Dir = repoDir
	if err := supervisor.run(checkout); err != nil {
		ctx.Info("erun: could not check out " + ref + "; runtime source left at the default branch")
	}
}

func describeRuntimeReleaseLink(boot runtimeBoot) string {
	if boot.get("ERUN_ENV_TYPE") != string(configpkg.EnvironmentTypeRuntime) || boot.flag("ERUN_REPO_REMOTE") {
		return ""
	}
	return "link " + boot.repoDir() + " to " + boot.releaseDir + " when the image bakes one"
}

// linkRuntimeRelease applies only to sourceless runtime environments.
func linkRuntimeRelease(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	_, err := LinkRuntimeRelease(boot.repoDir(), boot.releaseDir)
	return err
}
