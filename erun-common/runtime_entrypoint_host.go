package eruncommon

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
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

// writeRuntimeKubeconfig points kubectl at the cluster the pod runs in, through
// its own service account. The outer cloud-context name (for example
// `erun-001-020362606330-eu-west-2`) is added as a second context on the same
// cluster: the desktop-synced env config and any lookup that knows the outer
// name run `kubectl --context <outer-name>` from inside the pod, which would
// otherwise fail with "context does not exist".
func writeRuntimeKubeconfig(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	tokenFile := filepath.Join(boot.serviceAccountDir, "token")
	caFile := filepath.Join(boot.serviceAccountDir, "ca.crt")
	namespaceFile := filepath.Join(boot.serviceAccountDir, "namespace")
	for _, required := range []string{tokenFile, caFile, namespaceFile} {
		if _, err := os.Stat(required); err != nil {
			return nil // no mounted service account: nothing to point at
		}
	}
	namespace, err := os.ReadFile(namespaceFile)
	if err != nil {
		return nil
	}
	server := "https://" + boot.get("KUBERNETES_SERVICE_HOST") + ":" + boot.getOr("KUBERNETES_SERVICE_PORT_HTTPS", "443")
	contexts := []string{inClusterKubernetesContext}
	outer := boot.getOr("ERUN_CLOUD_CONTEXT_NAME", boot.get("ERUN_KUBERNETES_CONTEXT"))
	if outer != "" && outer != inClusterKubernetesContext {
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

// normalizeRuntimeSSHPermissions re-applies canonical modes to ~/.ssh. The pod no
// longer carries a fsGroup, but an environment that once ran with one still
// holds the g+rw the kubelet ORed into every home-volume file, and ssh refuses a
// private key looser than 0600. *.pub stays world-readable; everything else
// directly in ~/.ssh is treated as private.
func normalizeRuntimeSSHPermissions(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	dir := boot.path(".ssh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
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
	return nil
}

func describeRuntimeOutputsDir(boot runtimeBoot) string {
	return "create " + boot.outputsDir()
}

// ensureRuntimeOutputsDir creates the directory agents write deliverables to,
// which `erun outputs` lists. The image creates the default one; an overridden
// path may not exist yet.
func ensureRuntimeOutputsDir(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	return os.MkdirAll(boot.outputsDir(), 0o755)
}

func describeRuntimeGitSafeDirectory(boot runtimeBoot) string {
	return "mark " + boot.repoDir() + " as a git safe.directory"
}

// ensureRuntimeGitSafeDirectory lets git operate on a worktree owned by a foreign
// uid. A local-agent environment on Windows shares the repository into the WSL2
// node, where its files surface as root:root, and git's dubious-owner guard then
// aborts every in-pod `git status`. On a worktree erun already owns this is a
// no-op, so it is applied unconditionally and only once.
//
// Every child the boot starts goes through the supervisor, here and below: the
// init reaps each zombie it does not own, and a child it reaps before os/exec
// waits for it reports "no child processes" instead of its exit status.
func ensureRuntimeGitSafeDirectory(_ core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	if _, err := exec.LookPath("git"); err != nil {
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

// ensureRuntimeSource clones the environment's source into its worktree at boot
// for a runtime environment that opted into a mutable source mount. It clones
// only into an empty worktree, so live patches survive pod restarts, then checks
// out the deployed release ref. Failures are reported and the pod keeps serving
// without mounted source.
func ensureRuntimeSource(ctx core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	url := boot.get("ERUN_REPO_URL")
	repoDir := boot.repoDir()
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return err
	}
	if err := supervisor.run(core.Command("git", "clone", url, repoDir)); err != nil {
		ctx.Info("erun: failed to clone runtime source from " + url + "; continuing without mounted source")
		return nil
	}
	ref := boot.get("ERUN_REPO_REF")
	if ref == "" {
		return nil
	}
	checkout := core.Command("git", "checkout", "-q", ref)
	checkout.Dir = repoDir
	if err := supervisor.run(checkout); err != nil {
		ctx.Info("erun: could not check out " + ref + "; runtime source left at the default branch")
	}
	return nil
}

func describeRuntimeReleaseLink(boot runtimeBoot) string {
	if boot.get("ERUN_ENV_TYPE") != string(configpkg.EnvironmentTypeRuntime) || boot.flag("ERUN_REPO_REMOTE") {
		return ""
	}
	return "link " + boot.repoDir() + " to " + boot.releaseDir + " when the image bakes one"
}

// linkRuntimeRelease surfaces the image's baked release tree through the git
// folder of a sourceless runtime environment. Agent environments and runtime
// environments that mount source carry a real worktree and are not linked.
func linkRuntimeRelease(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	_, err := LinkRuntimeRelease(boot.repoDir(), boot.releaseDir)
	return err
}
