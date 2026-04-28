package cmd

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

var (
	ideExecCommand                   = exec.Command
	ideLookPath                      = exec.LookPath
	ideSleep                         = time.Sleep
	ideUserHomeDir                   = os.UserHomeDir
	ideGlob                          = filepath.Glob
	ideStat                          = os.Stat
	ideReadFile                      = os.ReadFile
	ideWriteFile                     = os.WriteFile
	ideMkdirAll                      = os.MkdirAll
	ideOpenURIFunc                   = openIDEURICommand
	ideStartURIFunc                  = startIDEURICommand
	runJetBrainsBootstrapAttemptFunc = runJetBrainsBootstrapAttempt
	openInstalledIntelliJAppFunc     = openInstalledIntelliJApp
	openIntelliJGatewayProjectFunc   = openIntelliJGatewayProject
	registerIntelliJProjectFunc      = registerIntelliJProject
	ideRunTokenFunc                  = newIDEOpenToken
)

type (
	VSCodeLauncher   func(common.Context, common.OpenResult) error
	IntelliJLauncher func(common.Context, common.OpenResult, PromptRunner) error
)

func launchVSCode(ctx common.Context, result common.OpenResult) error {
	return launchIDEURI(ctx, result, vscodeRemoteFolderURI(result))
}

func launchIntelliJ(ctx common.Context, result common.OpenResult, _ PromptRunner) error {
	hostOS := currentHostOS()
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	if err := registerIntelliJProjectFunc(ctx, result, hostOS); err != nil {
		return err
	}
	if opened, openErr := openIntelliJGatewayProjectFunc(ctx, result, hostOS); openErr != nil {
		return openErr
	} else if opened {
		return nil
	}
	if openErr := openInstalledIntelliJAppFunc(ctx, result, hostOS); openErr != nil {
		return formatIDEOpenError("IntelliJ IDEA", hostOS, result, "", openErr, "")
	}
	emitIntelliJManualOpenGuidance(ctx, result)
	return nil
}

func launchIDEURI(ctx common.Context, result common.OpenResult, uri string) error {
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	command, args, err := ideLaunchCommand(currentHostOS(), uri)
	if err != nil {
		return err
	}
	ctx.TraceCommand("", command, args...)
	if ctx.DryRun {
		return nil
	}
	technical, err := ideOpenURIFunc(ctx, command, args)
	if err != nil {
		return formatIDEOpenError("IDE", currentHostOS(), result, technical, err, "")
	}
	return nil
}

func vscodeRemoteFolderURI(result common.OpenResult) string {
	info := common.SSHConnectionInfoForResult(result)
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(info.HostAlias) + (&url.URL{Path: info.WorkspacePath}).EscapedPath()
}

func ideLaunchCommand(hostOS common.HostOS, uri string) (string, []string, error) {
	switch hostOS {
	case common.HostOSDarwin:
		return "open", []string{uri}, nil
	case common.HostOSLinux:
		return "xdg-open", []string{uri}, nil
	case common.HostOSWindows:
		return "cmd", []string{"/c", "start", "", uri}, nil
	default:
		return "", nil, fmt.Errorf("opening IDE links is unsupported on %s", hostOS)
	}
}

func openIDEURICommand(ctx common.Context, command string, args []string) (string, error) {
	cmd := ideExecCommand(command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = ctx.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	technical := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(stderr.String()),
		strings.TrimSpace(stdout.String()),
	}, "\n"))
	if technical == "" && err != nil {
		technical = err.Error()
	}
	return technical, err
}

func startIDEURICommand(ctx common.Context, command string, args []string) (string, error) {
	cmd := ideExecCommand(command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = nil
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Start()
	technical := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(stderr.String()),
		strings.TrimSpace(stdout.String()),
	}, "\n"))
	if technical == "" && err != nil {
		technical = err.Error()
	}
	if err != nil {
		return technical, err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return "", nil
}

func formatIDEOpenError(ideName string, hostOS common.HostOS, result common.OpenResult, technical string, err error, extra string) error {
	parts := []string{
		fmt.Sprintf("%s launcher failed on %s.", ideName, hostOSLabel(hostOS)),
		fmt.Sprintf("Fallback: connect manually in JetBrains over SSH to host %q and open %q.", common.SSHConnectionInfoForResult(result).HostAlias, common.SSHConnectionInfoForResult(result).WorkspacePath),
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		parts = append(parts, extra)
	}
	if technical = strings.TrimSpace(technical); technical != "" {
		parts = append(parts, "Technical error: "+technical)
	} else if err != nil {
		parts = append(parts, "Technical error: "+err.Error())
	}
	return errors.New(strings.Join(parts, "\n"))
}

func hostOSLabel(hostOS common.HostOS) string {
	switch hostOS {
	case common.HostOSDarwin:
		return "macOS"
	case common.HostOSLinux:
		return "Linux"
	case common.HostOSWindows:
		return "Windows"
	default:
		return string(hostOS)
	}
}

type jetbrainsBootstrapAttempt struct {
	command string
	args    []string
	runSync bool
}

func runJetBrainsBootstrapAttempt(ctx common.Context, attempt jetbrainsBootstrapAttempt) error {
	ctx.TraceCommand("", attempt.command, attempt.args...)
	if ctx.DryRun {
		return nil
	}
	cmd := ideExecCommand(attempt.command, attempt.args...)
	cmd.Stdin = nil
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	if attempt.runSync {
		if err := cmd.Run(); err != nil {
			return err
		}
	} else {
		if err := cmd.Start(); err != nil {
			return err
		}
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}
	ideSleep(2 * time.Second)
	return nil
}

func openInstalledIntelliJApp(ctx common.Context, _ common.OpenResult, hostOS common.HostOS) error {
	attempt, ok := resolveInstalledIntelliJAttempt(hostOS)
	if !ok {
		return fmt.Errorf("IntelliJ IDEA is not available on this host")
	}
	return runJetBrainsBootstrapAttemptFunc(ctx, attempt)
}

func openIntelliJGatewayProject(ctx common.Context, result common.OpenResult, hostOS common.HostOS) (bool, error) {
	optionsDir, err := resolveIntelliJOptionsDir(hostOS)
	if err != nil {
		return false, nil
	}
	info := common.SSHConnectionInfoForResult(result)
	recent, found, err := jetbrainsconfig.FindRecentProject(optionsDir, jetbrainsconfig.StableConfigID(info.HostAlias), info.WorkspacePath)
	if err != nil {
		return false, err
	}
	uri, ok := intelliJGatewayProjectURI(recent)
	if !found || !ok {
		return false, nil
	}
	command, args, err := intelliJGatewayLaunchCommand(hostOS, optionsDir, uri)
	if err != nil {
		return true, formatIDEOpenError("IntelliJ IDEA", hostOS, result, "", err, "")
	}
	ctx.TraceCommand("", command, args...)
	if ctx.DryRun {
		return true, nil
	}
	technical, err := ideStartURIFunc(ctx, command, args)
	if err != nil {
		return true, formatIDEOpenError("IntelliJ IDEA", hostOS, result, technical, err, "")
	}
	return true, nil
}

func intelliJGatewayLaunchCommand(hostOS common.HostOS, optionsDir string, uri string) (string, []string, error) {
	switch hostOS {
	case common.HostOSDarwin:
		return intelliJGatewayDarwinLaunchCommand(optionsDir, uri)
	default:
		return ideLaunchCommand(hostOS, uri)
	}
}

func intelliJGatewayDarwinLaunchCommand(optionsDir string, uri string) (string, []string, error) {
	contentsDir, err := resolveInstalledIntelliJContentsDir(common.HostOSDarwin)
	if err != nil {
		return "", nil, err
	}
	gatewayConfigDir, err := ensureIntelliJGatewayConfigDir(optionsDir)
	if err != nil {
		return "", nil, err
	}
	gatewaySystemDir := filepath.Join(filepath.Dir(gatewayConfigDir), "system")
	if err := ideMkdirAll(gatewaySystemDir, 0o700); err != nil {
		return "", nil, err
	}
	logDir, err := intelliJGatewayLogDir(optionsDir)
	if err != nil {
		return "", nil, err
	}
	if err := ideMkdirAll(logDir, 0o700); err != nil {
		return "", nil, err
	}

	java := filepath.Join(contentsDir, "jbr", "Contents", "Home", "bin", "java")
	pluginDir := filepath.Join(contentsDir, "plugins", "gateway-plugin")
	gatewayClassPath := strings.Join(intelliJGatewayStandaloneJars(pluginDir), string(os.PathListSeparator))
	classPath, err := intelliJGatewayClassPath(contentsDir, pluginDir)
	if err != nil {
		return "", nil, err
	}
	args := []string{
		"-Didea.application.info.value=" + filepath.Join(gatewayConfigDir, "info.xml"),
		"-Dcom.jetbrains.gateway.plugin.path.for.remote.dev.workers=" + pluginDir,
		"-Didea.additional.classpath=" + gatewayClassPath,
	}
	args = append(args, intelliJGatewayVMOptions(filepath.Join(pluginDir, "resources", "gateway.vmoptions"))...)
	args = append(args,
		"-Didea.platform.prefix=Gateway",
		"-Didea.parent.product=idea",
		"-Didea.config.path="+gatewayConfigDir,
		"-Didea.system.path="+gatewaySystemDir,
		"-Didea.log.path="+logDir,
		"-Dgateway.trusted.host.ui.not.changeable=true",
		"-Djna.boot.library.path="+filepath.Join(contentsDir, "lib", "jna", "aarch64"),
		"-Dpty4j.preferred.native.folder="+filepath.Join(contentsDir, "lib", "pty4j"),
		"-Dintellij.platform.runtime.repository.path="+filepath.Join(contentsDir, "modules", "module-descriptors.dat"),
		"-Dskiko.library.path="+filepath.Join(contentsDir, "lib", "skiko-awt-runtime-all"),
		"-Dfile.encoding=UTF-8",
		"-Dsun.stdout.encoding=UTF-8",
		"-Dsun.stderr.encoding=UTF-8",
		"-classpath",
		classPath,
		"com.intellij.idea.Main",
		uri,
	)
	return java, args, nil
}

func intelliJGatewayClassPath(contentsDir string, pluginDir string) (string, error) {
	entries := intelliJGatewayStandaloneJars(pluginDir)
	libJars, err := ideGlob(filepath.Join(contentsDir, "lib", "*.jar"))
	if err != nil {
		return "", err
	}
	slices.Sort(libJars)
	if len(libJars) == 0 {
		return "", fmt.Errorf("IntelliJ IDEA lib jars were not found under %s", filepath.Join(contentsDir, "lib"))
	}
	entries = append(entries, libJars...)
	return strings.Join(entries, string(os.PathListSeparator)), nil
}

func intelliJGatewayStandaloneJars(pluginDir string) []string {
	return []string{
		filepath.Join(pluginDir, "lib", "gateway-standalone", "gateway.core.jar"),
		filepath.Join(pluginDir, "lib", "gateway-standalone", "gateway.jar"),
	}
}

func intelliJGatewayVMOptions(path string) []string {
	data, err := ideReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	options := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		options = append(options, strings.TrimRight(line, "'"))
	}
	return options
}

func intelliJGatewayProjectURI(recent jetbrainsconfig.RecentProject) (string, bool) {
	if strings.TrimSpace(recent.ConfigID) == "" ||
		strings.TrimSpace(recent.ProjectPath) == "" ||
		strings.TrimSpace(recent.LatestUsedIDE.PathToIDE) == "" ||
		strings.TrimSpace(recent.LatestUsedIDE.BuildNumber) == "" {
		return "", false
	}
	productCode := strings.TrimSpace(recent.LatestUsedIDE.ProductCode)
	if productCode == "" {
		productCode = strings.TrimSpace(recent.ProductCode)
	}
	if productCode == "" {
		productCode = "IU"
	}
	values := url.Values{}
	values.Set("type", "ssh")
	values.Set("ssh", recent.ConfigID)
	values.Set("projectPath", recent.ProjectPath)
	values.Set("deploy", "false")
	values.Set("idePath", recent.LatestUsedIDE.PathToIDE)
	values.Set("buildNumber", recent.LatestUsedIDE.BuildNumber)
	values.Set("productCode", productCode)
	values.Set("runFromIdeToken", ideRunTokenFunc())
	return "jetbrains-gateway://connect#" + values.Encode(), true
}

func newIDEOpenToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

func registerIntelliJProject(ctx common.Context, result common.OpenResult, hostOS common.HostOS) error {
	optionsDir, err := resolveIntelliJOptionsDir(hostOS)
	if err != nil {
		return nil
	}
	info := common.SSHConnectionInfoForResult(result)
	entry := jetbrainsconfig.ProjectEntry{
		ConfigID:       jetbrainsconfig.StableConfigID(info.HostAlias),
		HostAlias:      info.HostAlias,
		User:           info.User,
		IdentityFile:   info.PrivateKeyPath,
		ProjectPath:    info.WorkspacePath,
		Port:           info.Port,
		ProductCode:    "IU",
		TimestampMilli: time.Now().UnixMilli(),
	}
	ctx.Trace(fmt.Sprintf("write IntelliJ SSH project config in %s", optionsDir))
	if ctx.DryRun {
		return nil
	}
	if err := jetbrainsconfig.UpsertOptionsFiles(optionsDir, entry); err != nil {
		return err
	}
	if hostOS == common.HostOSDarwin {
		gatewayOptionsDir, err := resolveIntelliJGatewayOptionsDir(optionsDir)
		if err == nil {
			if err := jetbrainsconfig.UpsertOptionsFiles(gatewayOptionsDir, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveInstalledIntelliJAttempt(hostOS common.HostOS) (jetbrainsBootstrapAttempt, bool) {
	switch hostOS {
	case common.HostOSDarwin:
		return jetbrainsBootstrapAttempt{
			command: "open",
			args:    []string{"-a", "IntelliJ IDEA"},
			runSync: true,
		}, true
	case common.HostOSLinux:
		attempts := lookPathBootstrapAttempts([]string{"idea", "idea64", "intellij-idea-ultimate"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	case common.HostOSWindows:
		attempts := lookPathBootstrapAttempts([]string{"idea64.exe", "idea.exe"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	}
	return jetbrainsBootstrapAttempt{}, false
}

func resolveInstalledIntelliJContentsDir(hostOS common.HostOS) (string, error) {
	switch hostOS {
	case common.HostOSDarwin:
		homeDir, _ := ideUserHomeDir()
		patterns := []string{
			"/Applications/IntelliJ IDEA*.app/Contents",
		}
		if strings.TrimSpace(homeDir) != "" {
			patterns = append(patterns, filepath.Join(homeDir, "Applications", "IntelliJ IDEA*.app", "Contents"))
		}
		for _, pattern := range patterns {
			matches, err := ideGlob(pattern)
			if err != nil {
				continue
			}
			slices.Sort(matches)
			for _, match := range matches {
				info, err := ideStat(match)
				if err == nil && info.IsDir() {
					return match, nil
				}
			}
		}
		return "", fmt.Errorf("IntelliJ IDEA.app was not found")
	default:
		return "", fmt.Errorf("IntelliJ IDEA app discovery is unsupported on %s", hostOS)
	}
}

func emitIntelliJManualOpenGuidance(ctx common.Context, result common.OpenResult) {
	out := ctx.Stderr
	if out == nil {
		out = ctx.Stdout
	}
	if out == nil {
		return
	}
	info := common.SSHConnectionInfoForResult(result)
	_, _ = fmt.Fprintf(out,
		"Opened IntelliJ IDEA locally.\n"+
			"Complete the remote connection in IntelliJ:\n"+
			"  Remote Development -> SSH\n"+
			"  host: %s\n"+
			"  user: %s\n"+
			"  key: %s\n"+
			"  project: %s\n",
		info.HostAlias,
		info.User,
		valueOrNone(info.PrivateKeyPath),
		info.WorkspacePath,
	)
}

func lookPathBootstrapAttempts(candidates []string) []jetbrainsBootstrapAttempt {
	attempts := make([]jetbrainsBootstrapAttempt, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		path, err := ideLookPath(candidate)
		if err != nil {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		attempts = append(attempts, jetbrainsBootstrapAttempt{
			command: path,
			runSync: false,
		})
	}
	return attempts
}

func resolveIntelliJOptionsDir(hostOS common.HostOS) (string, error) {
	homeDir, err := ideUserHomeDir()
	if err != nil {
		return "", err
	}

	var baseDir string
	switch hostOS {
	case common.HostOSDarwin:
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "JetBrains")
	case common.HostOSLinux:
		baseDir = filepath.Join(homeDir, ".config", "JetBrains")
	case common.HostOSWindows:
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			baseDir = filepath.Join(appData, "JetBrains")
		}
	default:
		return "", fmt.Errorf("unsupported host OS: %s", hostOS)
	}
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("JetBrains options directory base is not configured")
	}

	matches, err := ideGlob(filepath.Join(baseDir, "IntelliJIdea*", "options"))
	if err != nil {
		return "", err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(matches))
	for _, match := range matches {
		info, err := ideStat(match)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{path: match, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("IntelliJ IDEA options directory not found under %s", baseDir)
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.modTime.Equal(b.modTime) {
			return strings.Compare(b.path, a.path)
		}
		if a.modTime.After(b.modTime) {
			return -1
		}
		return 1
	})
	return candidates[0].path, nil
}

func resolveIntelliJGatewayOptionsDir(optionsDir string) (string, error) {
	optionsDir = filepath.Clean(strings.TrimSpace(optionsDir))
	if filepath.Base(optionsDir) != "options" {
		return "", fmt.Errorf("IntelliJ options directory must end with options: %s", optionsDir)
	}
	versionDir := filepath.Base(filepath.Dir(optionsDir))
	if strings.TrimSpace(versionDir) == "" || versionDir == "." || versionDir == string(filepath.Separator) {
		return "", fmt.Errorf("IntelliJ version directory not found for %s", optionsDir)
	}
	homeDir, err := ideUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "Library", "Caches", "JetBrains", versionDir, "tmp", "JetBrainsGateway", "config", "options"), nil
}

func ensureIntelliJGatewayConfigDir(optionsDir string) (string, error) {
	gatewayOptionsDir, err := resolveIntelliJGatewayOptionsDir(optionsDir)
	if err != nil {
		return "", err
	}
	gatewayConfigDir := filepath.Dir(gatewayOptionsDir)
	if err := ideMkdirAll(gatewayOptionsDir, 0o700); err != nil {
		return "", err
	}
	infoPath := filepath.Join(gatewayConfigDir, "info.xml")
	if _, err := ideStat(infoPath); err == nil {
		return gatewayConfigDir, nil
	}
	if err := ideWriteFile(infoPath, []byte(intelliJGatewayInfoXML()), 0o600); err != nil {
		return "", err
	}
	return gatewayConfigDir, nil
}

func intelliJGatewayLogDir(optionsDir string) (string, error) {
	versionDir := filepath.Base(filepath.Dir(filepath.Clean(optionsDir)))
	homeDir, err := ideUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "Library", "Logs", "JetBrains", versionDir, "gateway", time.Now().Format("20060102-150405")), nil
}

func intelliJGatewayInfoXML() string {
	return `<component xmlns="http://jetbrains.org/intellij/schema/application-info">
  <version major="2025" minor="3.2" eap="false"/>
  <build number="GW-253.30387.90"/>
  <company name="JetBrains s.r.o." url="https://www.jetbrains.com/"/>
  <names product="Gateway" fullname="JetBrains Gateway" script="gateway" motto="Develop together with pleasure!"/>
  <productUrl url="https://www.jetbrains.com/remote-development/gateway/"/>
  <logo url="/splash.png"/>
  <icon svg="/gateway.svg" svg-small="/gateway_16.svg"/>
  <icon-eap svg="/gateway_EAP.svg" svg-small="/gateway_16_EAP.svg"/>
</component>
`
}
