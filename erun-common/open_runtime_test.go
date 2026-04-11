package eruncommon

import (
	"strings"
	"testing"
)

func TestOpenRuntimeDockerArgsIncludeK3sSupportOnLinux(t *testing.T) {
	prevDetectHost := detectOpenRuntimeHost
	prevPathExists := hostPathExists
	t.Cleanup(func() {
		detectOpenRuntimeHost = prevDetectHost
		hostPathExists = prevPathExists
	})

	detectOpenRuntimeHost = func() HostRuntime {
		return HostRuntime{
			Host: HostInfo{
				OS:      HostOSLinux,
				HomeDir: "/home/tester",
			},
			ContainerRuntime:    ContainerRuntimeDocker,
			ContainerSocketPath: "/var/run/docker.sock",
			KubernetesInstallation: KubernetesInstallation{
				Type:           KubernetesInstallationK3s,
				KubeconfigPath: defaultK3sKubeconfigPath,
			},
		}
	}
	hostPathExists = func(path string) bool {
		switch path {
		case "/home/tester/.kube", "/home/tester/.ssh", "/home/tester/.gitconfig", "/home/tester/.docker", "/var/run/docker.sock", defaultK3sKubeconfigPath:
			return true
		default:
			return false
		}
	}

	args := openRuntimeDockerArgs(OpenResult{
		RepoPath: "/repo",
		Title:    "tenant-a-local",
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-local",
		},
	}, "/home/erun/git/repo", "erunpaas/tenant-a-devops:1.1.0")

	command := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm -it --network host",
		"-e ERUN_KUBERNETES_CONTEXT=cluster-local",
		"-e ERUN_SHELL_HOST=tenant-a-local",
		"-e KUBECONFIG=" + openRuntimeContainerKubeconfig + ":" + openRuntimeContainerK3sConfig,
		"-v /repo:/home/erun/git/repo",
		"-v /var/run/docker.sock:/var/run/docker.sock",
		"-v " + defaultK3sKubeconfigPath + ":" + openRuntimeContainerK3sConfig + ":ro",
		"erunpaas/tenant-a-devops:1.1.0 shell",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("expected runtime args to contain %q, got %q", want, command)
		}
	}
}

func TestOpenRuntimeDockerArgsConvertWindowsMounts(t *testing.T) {
	prevDetectHost := detectOpenRuntimeHost
	prevPathExists := hostPathExists
	t.Cleanup(func() {
		detectOpenRuntimeHost = prevDetectHost
		hostPathExists = prevPathExists
	})

	detectOpenRuntimeHost = func() HostRuntime {
		return HostRuntime{
			Host: HostInfo{
				OS:      HostOSWindows,
				HomeDir: `C:\Users\john`,
			},
			ContainerRuntime:    ContainerRuntimeDocker,
			ContainerSocketPath: `\\.\pipe\docker_engine`,
		}
	}
	hostPathExists = func(path string) bool {
		switch path {
		case `C:\Users\john\.kube`, `C:\Users\john\.ssh`, `C:\Users\john\.gitconfig`, `C:\Users\john\.docker`, `\\.\pipe\docker_engine`:
			return true
		default:
			return false
		}
	}

	args := openRuntimeDockerArgs(OpenResult{
		RepoPath: `C:\Users\john\project`,
		Title:    "tenant-a-local",
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-local",
		},
	}, "/home/erun/git/project", "erunpaas/tenant-a-devops:1.1.0")

	command := strings.Join(args, " ")
	for _, want := range []string{
		"-v /c/Users/john/project:/home/erun/git/project",
		"-v //./pipe/docker_engine:/var/run/docker.sock",
		"-v /c/Users/john/.kube:/home/erun/.kube:ro",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("expected runtime args to contain %q, got %q", want, command)
		}
	}
}
