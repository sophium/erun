package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// The pod's sshd listens on loopback only; the activity ssh-proxy in front of it
// is what the chart exposes, so every SSH byte is counted as operator activity
// before it reaches sshd.
const runtimeSSHDPort = "17023"

func (b runtimeBoot) sshdProxyPort() string {
	return b.getOr("ERUN_SSHD_PORT", "17022")
}

func describeRuntimeSSHD(boot runtimeBoot) string {
	if !boot.flag("ERUN_SSHD_ENABLED") {
		return ""
	}
	return "start sshd on 127.0.0.1:" + runtimeSSHDPort + " behind the activity ssh-proxy on 0.0.0.0:" + boot.sshdProxyPort()
}

// startRuntimeSSHD starts sshd and its activity proxy unless a live one already
// holds the pid file. The pid files are on the home volume and survive the pod,
// while every pod gets a fresh pid namespace that recycles low pids, so a pid is
// only trusted when /proc says the process is the expected program.
func startRuntimeSSHD(_ core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	sshdDir := boot.path(".sshd")
	hostKeyDir := filepath.Join(sshdDir, "host_keys")
	for _, dir := range []string{boot.path(".ssh"), hostKeyDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	for _, dir := range []string{boot.path(".ssh"), sshdDir, hostKeyDir} {
		_ = os.Chmod(dir, 0o700)
	}
	pidFile := filepath.Join(sshdDir, "sshd.pid")
	if !runtimePIDFileRunning(boot.procRoot, pidFile, "sshd") {
		_ = os.Remove(pidFile)
		if err := launchRuntimeSSHD(boot, sshdDir, hostKeyDir, pidFile, supervisor); err != nil {
			return err
		}
	}
	return startRuntimeSSHProxy(boot, sshdDir, supervisor)
}

func launchRuntimeSSHD(boot runtimeBoot, sshdDir, hostKeyDir, pidFile string, supervisor *runtimeSupervisor) error {
	hostKey := filepath.Join(hostKeyDir, "ssh_host_ed25519_key")
	if _, err := os.Stat(hostKey); err != nil {
		if err := supervisor.run(core.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey)); err != nil {
			return fmt.Errorf("generate the sshd host key: %w", err)
		}
	}
	_ = os.Chmod(hostKey, 0o600)
	_ = os.Chmod(hostKey+".pub", 0o644)

	authorizedKeys := boot.path(".ssh", "authorized_keys")
	configFile := filepath.Join(sshdDir, "sshd_config")
	config := strings.Join([]string{
		"Port " + runtimeSSHDPort,
		"ListenAddress 127.0.0.1",
		"HostKey " + hostKey,
		"AuthorizedKeysFile " + authorizedKeys,
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"ChallengeResponseAuthentication no",
		"PubkeyAuthentication yes",
		"StrictModes no",
		"PermitRootLogin no",
		"UsePAM no",
		"PidFile " + pidFile,
		"PrintMotd no",
		"Subsystem sftp internal-sftp",
	}, "\n") + "\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		return err
	}
	if err := touchRuntimeFile(authorizedKeys, 0o600); err != nil {
		return err
	}
	// sshd daemonises: this waits only for its parent, and the daemon itself is
	// reparented here, where the init loop reaps its per-connection children.
	return supervisor.run(core.Command(boot.sshdPath, "-f", configFile, "-E", filepath.Join(sshdDir, "sshd.log")))
}

func startRuntimeSSHProxy(boot runtimeBoot, sshdDir string, supervisor *runtimeSupervisor) error {
	pidFile := filepath.Join(sshdDir, "ssh-proxy.pid")
	if runtimePIDFileRunning(boot.procRoot, pidFile, "ssh-proxy") {
		return nil
	}
	_ = os.Remove(pidFile)
	log, err := os.OpenFile(filepath.Join(sshdDir, "ssh-proxy.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := core.Command(boot.erunPath, "activity", "ssh-proxy",
		"--tenant", boot.tenant(),
		"--environment", boot.environment(),
		"--listen", "0.0.0.0:"+boot.sshdProxyPort(),
		"--target", "127.0.0.1:"+runtimeSSHDPort,
		"--idle-traffic-bytes", boot.getOr("ERUN_IDLE_TRAFFIC_BYTES", "0"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := supervisor.start(cmd); err != nil {
		_ = log.Close()
		return err
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		return err
	}
	go func() {
		_ = supervisor.wait(cmd)
		_ = log.Close()
	}()
	return nil
}

// runtimePIDFileRunning reports whether pidFile names a live process whose
// command line contains want.
func runtimePIDFileRunning(procRoot, pidFile, want string) bool {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ReplaceAll(string(cmdline), "\x00", " "), want)
}

func touchRuntimeFile(path string, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
