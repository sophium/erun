package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
)

func finishRemoteInitProjectRoot(ctx core.Context, path string) error {
	ctx.TraceCommand("", "mkdir", "-p", path)
	if ctx.DryRun {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

// finishRemoteInitSkills reinstalls the baked agent skills, leaving any whose
// SKILL.md already exists so in-env edits survive.
func finishRemoteInitSkills(ctx core.Context, homeDir string) error {
	root := bakedSkillsRoot(os.Getenv)
	entries := readBakedSkillEntries(root)
	for _, destRoot := range []string{filepath.Join(homeDir, ".claude", "skills"), filepath.Join(homeDir, ".codex", "skills")} {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			src := filepath.Join(root, entry.Name())
			dst := filepath.Join(destRoot, entry.Name())
			srcContents := src + string(os.PathSeparator) + "."
			ctx.TraceCommand("", "cp", "-R", srcContents, dst)
			if ctx.DryRun {
				continue
			}
			if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err == nil {
				continue
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			capture := ctx.ToolCapture()
			cmd := core.Command("cp", "-R", srcContents, dst+string(os.PathSeparator))
			cmd.Stdout = capture.Stdout()
			cmd.Stderr = capture.Stderr()
			if err := capture.Apply(cmd.Run()); err != nil {
				return err
			}
		}
	}
	return nil
}

func readBakedSkillEntries(root string) []os.DirEntry {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	return entries
}

func finishRemoteInitSSHKey(ctx core.Context, path string) error {
	ctx.TraceCommand("", "ssh-keygen", "-t", "ed25519", "-N", "", "-f", path)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := core.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", path)
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	if err := capture.Apply(cmd.Run()); err != nil {
		return err
	}
	return ensureRemoteInitSSHKeyPermissions(path)
}

// finishRemoteInitCodeCommitSSHKey generates the RSA 4096 keypair CodeCommit requires.
func finishRemoteInitCodeCommitSSHKey(ctx core.Context, path string) error {
	ctx.TraceCommand("", "ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", path)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := core.Command("ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", path)
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	if err := capture.Apply(cmd.Run()); err != nil {
		return err
	}
	return ensureRemoteInitSSHKeyPermissions(path)
}

// finishRemoteInitCodeCommitSSHConfig writes the ~/.ssh/config stanza without
// which CodeCommit cannot resolve the IAM identity.
func finishRemoteInitCodeCommitSSHConfig(ctx core.Context, homeDir string, repository remoteinit.RemoteRepositorySpec) error {
	if repository.CodeCommitHost == "" {
		return nil
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")
	ctx.TraceCommand("", "write-file", configPath)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	content := remoteinit.CodeCommitSSHConfig(repository, repository.CodeCommitSSHKeyID) + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return err
	}
	return nil
}

// ensureRemoteInitSSHKeyPermissions re-tightens key modes: ssh refuses a
// group-accessible key, and old PVCs still carry fsGroup's g+rw.
func ensureRemoteInitSSHKeyPermissions(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("chmod 0600 %s: %w", path, err)
	}
	pub := path + ".pub"
	if err := os.Chmod(pub, 0o644); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("chmod 0644 %s: %w", pub, err)
	}
	return nil
}

func finishRemoteInitGitCheckout(ctx core.Context, projectRoot, sshKeyPath string, repository remoteinit.RemoteRepositorySpec) error {
	if strings.TrimSpace(repository.URL) == "" {
		return errors.New("repository URL is required to finish git checkout")
	}
	ctx.TraceCommand(projectRoot, "git", "clone", repository.URL, ".")
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := core.Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommandFor(repository, sshKeyPath), "clone", repository.URL, ".")
	cmd.Dir = projectRoot
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}

// finishRemoteInitGitAccess prints the public key to import and polls
// `git ls-remote` until access is active.
func finishRemoteInitGitAccess(ctx core.Context, sshKeyPath, codeCommitKeyPath string, repository remoteinit.RemoteRepositorySpec, sshKeyJustGenerated, codeCommitKeyJustGenerated bool, sleep core.SleepFunc) error {
	sshCommand := doctorRemoteInitGitSSHCommandFor(repository, sshKeyPath)
	if ctx.DryRun {
		ctx.TraceCommand("", "git", "-c", "core.sshCommand="+sshCommand, "ls-remote", repository.URL, "HEAD")
		return nil
	}
	if repository.CodeCommitHost != "" {
		if err := announceRemoteInitCodeCommitKey(ctx, codeCommitKeyPath, repository, codeCommitKeyJustGenerated); err != nil {
			return err
		}
	} else if err := announceRemoteInitSSHKey(ctx, sshKeyPath, sshKeyJustGenerated); err != nil {
		return err
	}
	return gitx.WaitForGitAccess(ctx, sleep, func() error {
		capture := ctx.ToolCapture()
		cmd := core.Command("git", "-c", "core.sshCommand="+sshCommand, "ls-remote", repository.URL, "HEAD")
		cmd.Stdout = capture.Stdout()
		cmd.Stderr = capture.Stderr()
		return capture.Apply(cmd.Run())
	})
}

func announceRemoteInitCodeCommitKey(ctx core.Context, codeCommitKeyPath string, repository remoteinit.RemoteRepositorySpec, justGenerated bool) error {
	if codeCommitKeyPath == "" {
		return errors.New("CodeCommit SSH key path is required to finish CodeCommit git access")
	}
	if err := ensureRemoteInitSSHKeyPermissions(codeCommitKeyPath); err != nil {
		return err
	}
	publicKey, err := readRemoteInitPublicKey(codeCommitKeyPath)
	if err != nil {
		return err
	}
	if justGenerated {
		ctx.Info("Generated CodeCommit SSH keypair.")
	}
	ctx.Info(remoteinit.CodeCommitSetupDetails(repository, publicKey, repository.CodeCommitSSHKeyID))
	return nil
}

func announceRemoteInitSSHKey(ctx core.Context, sshKeyPath string, justGenerated bool) error {
	if err := ensureRemoteInitSSHKeyPermissions(sshKeyPath); err != nil {
		return err
	}
	publicKey, err := readRemoteInitPublicKey(sshKeyPath)
	if err != nil {
		return err
	}
	if justGenerated {
		ctx.Info("Generated SSH keypair. Import this SSH public key into your git host before continuing:")
	} else {
		ctx.Info("Import this SSH public key into your git host before continuing:")
	}
	ctx.Info(publicKey)
	return nil
}

func readRemoteInitPublicKey(sshKeyPath string) (string, error) {
	data, err := os.ReadFile(sshKeyPath + ".pub")
	if err != nil {
		return "", fmt.Errorf("read SSH public key %s.pub: %w", sshKeyPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func defaultRemoteInitSSHKeyPath(homeDir string) string {
	return filepath.Join(homeDir, ".ssh", "id_ed25519")
}

func defaultRemoteInitCodeCommitSSHKeyPath(homeDir string) string {
	return filepath.Join(homeDir, ".ssh", "id_rsa_codecommit")
}

// doctorRemoteInitGitSSHCommandFor uses `ssh -F` for CodeCommit, since pinning
// an identity bypasses IAM user resolution; other hosts pin the ed25519 key.
func doctorRemoteInitGitSSHCommandFor(repository remoteinit.RemoteRepositorySpec, sshKeyPath string) string {
	if repository.CodeCommitHost != "" {
		return `ssh -F "$HOME/.ssh/config"`
	}
	return "ssh -i " + core.ShellQuote(sshKeyPath) + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
}
