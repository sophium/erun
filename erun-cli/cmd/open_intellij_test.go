package cmd

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

func TestLaunchIntelliJEnsuresKnownHostBeforeOpening(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevOpenGateway := openIntelliJGatewayProjectFunc
	prevRegister := registerIntelliJProjectFunc
	prevHostOS := currentHostOS
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		openIntelliJGatewayProjectFunc = prevOpenGateway
		registerIntelliJProjectFunc = prevRegister
		currentHostOS = prevHostOS
	})

	callOrder := make([]string, 0, 2)
	currentHostOS = func() common.HostOS { return common.HostOSDarwin }
	ensureLocalSSHDKnownHostFunc = func(_ common.Context, result common.OpenResult) error {
		callOrder = append(callOrder, "known_hosts")
		requireIntelliJTarget(t, result)
		return nil
	}
	registerIntelliJProjectFunc = func(_ common.Context, result common.OpenResult, hostOS common.HostOS) error {
		callOrder = append(callOrder, "register_project")
		requireIntelliJHostOS(t, hostOS)
		requireIntelliJTarget(t, result)
		return nil
	}
	openIntelliJGatewayProjectFunc = func(_ common.Context, result common.OpenResult, hostOS common.HostOS) (bool, error) {
		callOrder = append(callOrder, "open_gateway")
		requireIntelliJHostOS(t, hostOS)
		requireIntelliJTarget(t, result)
		return false, nil
	}
	openInstalledIntelliJAppFunc = func(_ common.Context, result common.OpenResult, hostOS common.HostOS) error {
		callOrder = append(callOrder, "open_intellij")
		requireIntelliJHostOS(t, hostOS)
		requireIntelliJTarget(t, result)
		return nil
	}

	stderr := new(bytes.Buffer)
	err := launchIntelliJ(common.Context{Stderr: stderr}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name: "tenant-a",
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:       true,
				LocalPort:     62222,
				PublicKeyPath: "/Users/test/.ssh/id_ed25519.pub",
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, nil)
	if err != nil {
		t.Fatalf("launchIntelliJ failed: %v", err)
	}
	if got := strings.Join(callOrder, ","); got != "known_hosts,register_project,open_gateway,open_intellij" {
		t.Fatalf("unexpected call order: %s", got)
	}
	for _, want := range []string{
		"Opened IntelliJ IDEA locally.",
		"Remote Development -> SSH",
		"host: erun-tenant-a-remote",
		"user: erun",
		"key: /Users/test/.ssh/id_ed25519",
		"project: /home/erun/git/tenant-a",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected guidance to contain %q, got:\n%s", want, stderr.String())
		}
	}
}

func requireIntelliJTarget(t *testing.T, result common.OpenResult) {
	t.Helper()
	if result.Tenant != "tenant-a" {
		t.Fatalf("unexpected target: %+v", result)
	}
}

func requireIntelliJHostOS(t *testing.T, hostOS common.HostOS) {
	t.Helper()
	if hostOS != common.HostOSDarwin {
		t.Fatalf("unexpected host OS: %s", hostOS)
	}
}

func TestLaunchIntelliJReturnsKnownHostError(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevOpenGateway := openIntelliJGatewayProjectFunc
	prevRegister := registerIntelliJProjectFunc
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		openIntelliJGatewayProjectFunc = prevOpenGateway
		registerIntelliJProjectFunc = prevRegister
	})

	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error {
		return errors.New("known host failed")
	}
	openInstalledIntelliJAppFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		t.Fatal("did not expect IntelliJ launch when known_hosts update fails")
		return nil
	}
	openIntelliJGatewayProjectFunc = func(common.Context, common.OpenResult, common.HostOS) (bool, error) {
		t.Fatal("did not expect gateway project launch when known_hosts update fails")
		return false, nil
	}
	registerIntelliJProjectFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		t.Fatal("did not expect JetBrains config registration when known_hosts update fails")
		return nil
	}

	err := launchIntelliJ(common.Context{}, common.OpenResult{}, nil)
	if err == nil || err.Error() != "known host failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchIntelliJReturnsInstalledIDEAError(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevOpenGateway := openIntelliJGatewayProjectFunc
	prevRegister := registerIntelliJProjectFunc
	prevHostOS := currentHostOS
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		openIntelliJGatewayProjectFunc = prevOpenGateway
		registerIntelliJProjectFunc = prevRegister
		currentHostOS = prevHostOS
	})

	currentHostOS = func() common.HostOS { return common.HostOSDarwin }
	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error { return nil }
	registerIntelliJProjectFunc = func(common.Context, common.OpenResult, common.HostOS) error { return nil }
	openIntelliJGatewayProjectFunc = func(common.Context, common.OpenResult, common.HostOS) (bool, error) {
		return false, nil
	}
	openInstalledIntelliJAppFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		return errors.New("IntelliJ IDEA is not available on this host")
	}

	err := launchIntelliJ(common.Context{}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name: "tenant-a",
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:   true,
				LocalPort: 62222,
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, nil)
	if err == nil {
		t.Fatal("expected missing IntelliJ IDEA error")
	}
	for _, want := range []string{
		"IntelliJ IDEA launcher failed on macOS.",
		`host "erun-tenant-a-remote"`,
		"IntelliJ IDEA is not available on this host",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err.Error())
		}
	}
}

func TestLaunchIntelliJOpensGatewayProjectBeforeFallback(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevOpenGateway := openIntelliJGatewayProjectFunc
	prevRegister := registerIntelliJProjectFunc
	prevHostOS := currentHostOS
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		openIntelliJGatewayProjectFunc = prevOpenGateway
		registerIntelliJProjectFunc = prevRegister
		currentHostOS = prevHostOS
	})

	currentHostOS = func() common.HostOS { return common.HostOSDarwin }
	callOrder := make([]string, 0, 3)
	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error {
		callOrder = append(callOrder, "known_hosts")
		return nil
	}
	registerIntelliJProjectFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		callOrder = append(callOrder, "register_project")
		return nil
	}
	openIntelliJGatewayProjectFunc = func(common.Context, common.OpenResult, common.HostOS) (bool, error) {
		callOrder = append(callOrder, "open_gateway")
		return true, nil
	}
	openInstalledIntelliJAppFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		t.Fatal("did not expect local IntelliJ fallback after gateway project launch")
		return nil
	}

	stderr := new(bytes.Buffer)
	err := launchIntelliJ(common.Context{Stderr: stderr}, common.OpenResult{}, nil)
	if err != nil {
		t.Fatalf("launchIntelliJ failed: %v", err)
	}
	if got := strings.Join(callOrder, ","); got != "known_hosts,register_project,open_gateway" {
		t.Fatalf("unexpected call order: %s", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("did not expect manual guidance after gateway project launch, got:\n%s", stderr.String())
	}
}

func TestIntelliJGatewayProjectURI(t *testing.T) {
	prevToken := ideRunTokenFunc
	t.Cleanup(func() {
		ideRunTokenFunc = prevToken
	})
	ideRunTokenFunc = func() string { return "fixed-token" }

	got, ok := intelliJGatewayProjectURI(jetbrainsconfig.RecentProject{
		ConfigID:    "14feee13-47cc-53b1-957a-326051d70e86",
		ProjectPath: "/home/erun/git/petios",
		ProductCode: "IU",
		LatestUsedIDE: jetbrainsconfig.RecentProjectIDE{
			BuildNumber: "261.23567.71",
			PathToIDE:   "/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64",
			ProductCode: "IU",
		},
	})
	if !ok {
		t.Fatal("expected gateway URI to be available")
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URI: %v", err)
	}
	if parsed.Scheme != "jetbrains-gateway" || parsed.Host != "connect" {
		t.Fatalf("unexpected gateway URI: %s", got)
	}
	values, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatalf("parse gateway fragment: %v", err)
	}
	wants := map[string]string{
		"type":            "ssh",
		"ssh":             "14feee13-47cc-53b1-957a-326051d70e86",
		"projectPath":     "/home/erun/git/petios",
		"deploy":          "false",
		"idePath":         "/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64",
		"buildNumber":     "261.23567.71",
		"productCode":     "IU",
		"runFromIdeToken": "fixed-token",
	}
	for name, want := range wants {
		if values.Get(name) != want {
			t.Fatalf("unexpected %s: want %q, got %q in %s", name, want, values.Get(name), got)
		}
	}
}

func TestOpenIntelliJGatewayProjectLaunchesRecentProjectURI(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	prevStartURI := ideStartURIFunc
	prevToken := ideRunTokenFunc
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
		ideStartURIFunc = prevStartURI
		ideRunTokenFunc = prevToken
	})

	root := t.TempDir()
	optionsDir := filepath.Join(root, "Library", "Application Support", "JetBrains", "IntelliJIdea2025.3", "options")
	requireNoError(t, os.MkdirAll(optionsDir, 0o700), "mkdir optionsDir")
	appContentsDir := filepath.Join(root, "Applications", "IntelliJ IDEA.app", "Contents")
	requireNoError(t, os.MkdirAll(filepath.Join(appContentsDir, "plugins", "gateway-plugin", "resources"), 0o700), "mkdir app contents")
	requireNoError(t, os.MkdirAll(filepath.Join(appContentsDir, "lib"), 0o700), "mkdir app lib")
	requireNoError(t, os.WriteFile(filepath.Join(appContentsDir, "lib", "app.jar"), nil, 0o600), "write app jar")
	requireNoError(t, os.WriteFile(filepath.Join(appContentsDir, "plugins", "gateway-plugin", "resources", "gateway.vmoptions"), []byte("-Xmx512m\n-Dapple.awt.application.name=Gateway\n"), 0o600), "write gateway vmoptions")
	configID := jetbrainsconfig.StableConfigID("erun-tenant-a-remote")
	requireWriteFile(t, filepath.Join(optionsDir, "sshRecentConnections.v2.xml"), []byte(`<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="`+configID+`"></option>
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="date" value="1777362254961"></option>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="261.23567.71"></option>
                    <option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>
                    <option name="productCode" value="IU"></option>
                  </RecentProjectInstalledIde>
                </option>
                <option name="productCode" value="IU"></option>
                <option name="projectPath" value="/home/erun/git/tenant-a"></option>
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`), 0o600, "write recent projects")

	ideUserHomeDir = func() (string, error) { return root, nil }
	ideGlob = gatewayProjectGlob(optionsDir, appContentsDir)
	ideStat = os.Stat
	ideRunTokenFunc = func() string { return "fixed-token" }
	var launchedCommand string
	var launchedArgs []string
	ideStartURIFunc = func(_ common.Context, command string, args []string) (string, error) {
		launchedCommand = command
		launchedArgs = append([]string(nil), args...)
		return "", nil
	}

	opened, err := openIntelliJGatewayProject(common.Context{}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name: "tenant-a",
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:   true,
				LocalPort: 62222,
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, common.HostOSDarwin)
	requireNoError(t, err, "openIntelliJGatewayProject failed")
	requireGatewayProjectLaunch(t, opened, launchedCommand, launchedArgs, appContentsDir, configID)
}

func gatewayProjectGlob(optionsDir, appContentsDir string) func(string) ([]string, error) {
	return func(pattern string) ([]string, error) {
		if strings.Contains(pattern, "IntelliJIdea*") {
			return []string{optionsDir}, nil
		}
		if strings.Contains(pattern, "IntelliJ IDEA*.app") {
			return []string{appContentsDir}, nil
		}
		if strings.Contains(pattern, "lib/*.jar") {
			return []string{filepath.Join(appContentsDir, "lib", "app.jar")}, nil
		}
		return nil, nil
	}
}

func requireGatewayProjectLaunch(t *testing.T, opened bool, launchedCommand string, launchedArgs []string, appContentsDir, configID string) {
	t.Helper()
	if !opened {
		t.Fatal("expected gateway project launch")
	}
	if launchedCommand != filepath.Join(appContentsDir, "jbr", "Contents", "Home", "bin", "java") {
		t.Fatalf("unexpected launch command: %s %+v", launchedCommand, launchedArgs)
	}
	uri := launchedArgs[len(launchedArgs)-1]
	values := parseGatewayFragment(t, uri)
	if values.Get("ssh") != configID ||
		values.Get("projectPath") != "/home/erun/git/tenant-a" ||
		values.Get("idePath") != "/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64" ||
		values.Get("runFromIdeToken") != "fixed-token" {
		t.Fatalf("unexpected gateway URI values: %s", uri)
	}
	requireGatewayLaunchArgs(t, launchedArgs, appContentsDir)
}

func requireGatewayLaunchArgs(t *testing.T, launchedArgs []string, appContentsDir string) {
	t.Helper()
	if !strings.Contains(strings.Join(launchedArgs, "\n"), "-Didea.platform.prefix=Gateway") {
		t.Fatalf("expected Gateway platform launch args, got %+v", launchedArgs)
	}
	if !slices.Contains(launchedArgs, "-classpath") || !strings.Contains(strings.Join(launchedArgs, "\n"), filepath.Join(appContentsDir, "lib", "app.jar")) {
		t.Fatalf("expected IntelliJ app jars on Gateway classpath, got %+v", launchedArgs)
	}
}

func parseGatewayFragment(t *testing.T, uri string) url.Values {
	t.Helper()
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse gateway URI: %v", err)
	}
	if parsed.Scheme != "jetbrains-gateway" || parsed.Host != "connect" {
		t.Fatalf("unexpected gateway URI: %s", uri)
	}
	values, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatalf("parse gateway fragment: %v", err)
	}
	return values
}

func TestOpenInstalledIntelliJAppDryRunTracesLaunch(t *testing.T) {
	prevExec := ideExecCommand
	t.Cleanup(func() {
		ideExecCommand = prevExec
	})

	ideExecCommand = func(name string, args ...string) *exec.Cmd {
		t.Fatal("did not expect process execution during dry-run")
		return nil
	}

	stderr := new(bytes.Buffer)
	err := openInstalledIntelliJApp(common.Context{
		Logger: common.NewLoggerWithWriters(2, stderr, stderr),
		Stderr: stderr,
		DryRun: true,
	}, common.OpenResult{}, common.HostOSDarwin)
	if err != nil {
		t.Fatalf("openInstalledIntelliJApp failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "open -a 'IntelliJ IDEA'") {
		t.Fatalf("expected dry-run trace to contain IntelliJ launch, got:\n%s", stderr.String())
	}
}

func TestRegisterIntelliJProjectDryRunTracesOptionsDir(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	optionsDir := t.TempDir()
	ideUserHomeDir = func() (string, error) { return "/Users/test", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{optionsDir}, nil }
	ideStat = os.Stat

	stderr := new(bytes.Buffer)
	err := registerIntelliJProject(common.Context{
		Logger: common.NewLoggerWithWriters(2, stderr, stderr),
		Stderr: stderr,
		DryRun: true,
	}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name: "tenant-a",
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:       true,
				LocalPort:     62222,
				PublicKeyPath: "/Users/test/.ssh/id_ed25519.pub",
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, common.HostOSDarwin)
	if err != nil {
		t.Fatalf("registerIntelliJProject failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "write IntelliJ SSH project config in "+optionsDir) {
		t.Fatalf("expected dry-run trace to contain options dir, got:\n%s", stderr.String())
	}
}

func TestResolveIntelliJOptionsDirChoosesMostRecentlyModified(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	root := t.TempDir()
	oldDir := filepath.Join(root, "IntelliJIdea2025.2", "options")
	newDir := filepath.Join(root, "IntelliJIdea2025.3", "options")
	requireNoError(t, os.MkdirAll(oldDir, 0o700), "mkdir oldDir")
	requireNoError(t, os.MkdirAll(newDir, 0o700), "mkdir newDir")
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	requireNoError(t, os.Chtimes(oldDir, oldTime, oldTime), "chtimes oldDir")
	requireNoError(t, os.Chtimes(newDir, newTime, newTime), "chtimes newDir")

	ideUserHomeDir = func() (string, error) { return "/Users/test", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{oldDir, newDir}, nil }
	ideStat = os.Stat

	got, err := resolveIntelliJOptionsDir(common.HostOSDarwin)
	if err != nil {
		t.Fatalf("resolveIntelliJOptionsDir failed: %v", err)
	}
	if got != newDir {
		t.Fatalf("unexpected options dir: %q", got)
	}
}

func TestResolveIntelliJOptionsDirWindowsUsesAppData(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	root := t.TempDir()
	optionsDir := filepath.Join(root, "JetBrains", "IntelliJIdea2025.3", "options")
	requireNoError(t, os.MkdirAll(optionsDir, 0o700), "mkdir optionsDir")
	t.Setenv("APPDATA", root)

	ideUserHomeDir = func() (string, error) { return "/Users/ignored", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{optionsDir}, nil }
	ideStat = os.Stat

	got, err := resolveIntelliJOptionsDir(common.HostOSWindows)
	if err != nil {
		t.Fatalf("resolveIntelliJOptionsDir failed: %v", err)
	}
	if got != optionsDir {
		t.Fatalf("unexpected options dir: %q", got)
	}
}
