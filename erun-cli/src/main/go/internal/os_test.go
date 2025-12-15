package internal

import "testing"

func TestDockerMountPathWindows(t *testing.T) {
	info := OSInfo{Type: HostOSWindows}
	got := info.DockerMountPath(`C:\Users\john\project`)
	want := "/c/Users/john/project"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDockerVolumeSourceWindowsPipe(t *testing.T) {
	info := OSInfo{Type: HostOSWindows}
	got := info.DockerVolumeSource(`\\.\pipe\docker_engine`)
	want := "//./pipe/docker_engine"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestClassifyHostOS(t *testing.T) {
	if classifyHostOS("darwin") != HostOSDarwin {
		t.Fatalf("expected darwin classification")
	}
	if classifyHostOS("linux") != HostOSLinux {
		t.Fatalf("expected linux classification")
	}
	if classifyHostOS("windows") != HostOSWindows {
		t.Fatalf("expected windows classification")
	}
	if classifyHostOS("plan9") != HostOSUnknown {
		t.Fatalf("expected unknown classification")
	}
}

func TestDetectOSUsesCustomHome(t *testing.T) {
	prev := userHomeDir
	userHomeDir = func() (string, error) {
		return "/tmp/home", nil
	}
	t.Cleanup(func() {
		userHomeDir = prev
	})

	info := DetectOS()
	if info.HomeDir != "/tmp/home" {
		t.Fatalf("expected home override to be used, got %q", info.HomeDir)
	}
}
