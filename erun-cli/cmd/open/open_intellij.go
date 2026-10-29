package open

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/desktop"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

func LaunchIntelliJ(ctx core.Context, result config.OpenResult, _ prompt.PromptRunner) error {
	hostOS := desktop.CurrentHostOS()
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

type jetbrainsBootstrapAttempt struct {
	command string
	args    []string
	runSync bool
}

func runJetBrainsBootstrapAttempt(ctx core.Context, attempt jetbrainsBootstrapAttempt) error {
	ctx.TraceCommand("", attempt.command, attempt.args...)
	if ctx.DryRun {
		return nil
	}
	cmd := ideExecCommand(attempt.command, attempt.args...)
	core.HideConsoleWindow(cmd)
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

func openInstalledIntelliJApp(ctx core.Context, _ config.OpenResult, hostOS model.HostOS) error {
	attempt, ok := resolveInstalledIntelliJAttempt(hostOS)
	if !ok {
		return fmt.Errorf("IntelliJ IDEA is not available on this host")
	}
	return runJetBrainsBootstrapAttemptFunc(ctx, attempt)
}

func openIntelliJGatewayProject(ctx core.Context, result config.OpenResult, hostOS model.HostOS) (bool, error) {
	optionsDir, ok := resolvableIntelliJOptionsDir(hostOS)
	if !ok {
		return false, nil
	}
	info := sshconn.SSHConnectionInfoForResult(result)
	recent, found, err := jetbrainsconfig.FindRecentProject(optionsDir, jetbrainsconfig.StableConfigID(info.HostAlias), info.WorkspacePath)
	if err != nil {
		return false, err
	}
	uri, ok := intelliJGatewayProjectURI(recent)
	if !found || !ok {
		return false, nil
	}
	command, args, err := intelliJGatewayLaunchCommand(ctx, hostOS, optionsDir, uri)
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

func intelliJGatewayLaunchCommand(ctx core.Context, hostOS model.HostOS, optionsDir string, uri string) (string, []string, error) {
	if hostOS == model.HostOSDarwin {
		return intelliJGatewayDarwinLaunchCommand(ctx, optionsDir, uri)
	}
	return ideLaunchCommand(hostOS, uri)
}

func intelliJGatewayDarwinLaunchCommand(ctx core.Context, optionsDir string, uri string) (string, []string, error) {
	contentsDir, err := resolveInstalledIntelliJContentsDir(model.HostOSDarwin)
	if err != nil {
		return "", nil, err
	}
	gatewayConfigDir, err := ensureIntelliJGatewayConfigDir(ctx, optionsDir)
	if err != nil {
		return "", nil, err
	}
	gatewaySystemDir := filepath.Join(filepath.Dir(gatewayConfigDir), "system")
	ctx.TraceCommand("", "mkdir", "-p", gatewaySystemDir)
	if !ctx.DryRun {
		if err := ideMkdirAll(gatewaySystemDir, 0o700); err != nil {
			return "", nil, err
		}
	}
	logDir, err := intelliJGatewayLogDir(optionsDir)
	if err != nil {
		return "", nil, err
	}
	ctx.TraceCommand("", "mkdir", "-p", logDir)
	if !ctx.DryRun {
		if err := ideMkdirAll(logDir, 0o700); err != nil {
			return "", nil, err
		}
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
	libJars, err := desktop.IdeGlob(filepath.Join(contentsDir, "lib", "*.jar"))
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

func registerIntelliJProject(ctx core.Context, result config.OpenResult, hostOS model.HostOS) error {
	optionsDir, ok := resolvableIntelliJOptionsDir(hostOS)
	if !ok {
		return nil
	}
	info := sshconn.SSHConnectionInfoForResult(result)
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
	if hostOS == model.HostOSDarwin {
		gatewayOptionsDir, err := resolveIntelliJGatewayOptionsDir(optionsDir)
		if err == nil {
			if err := jetbrainsconfig.UpsertOptionsFiles(gatewayOptionsDir, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveInstalledIntelliJAttempt(hostOS model.HostOS) (jetbrainsBootstrapAttempt, bool) {
	switch hostOS {
	case model.HostOSDarwin:
		return jetbrainsBootstrapAttempt{
			command: "open",
			args:    []string{"-a", "IntelliJ IDEA"},
			runSync: true,
		}, true
	case model.HostOSLinux:
		attempts := lookPathBootstrapAttempts([]string{"idea", "idea64", "intellij-idea-ultimate"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	case model.HostOSWindows:
		attempts := lookPathBootstrapAttempts([]string{"idea64.exe", "idea.exe"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	case model.HostOSUnknown:
	}
	return jetbrainsBootstrapAttempt{}, false
}

func resolvableIntelliJOptionsDir(hostOS model.HostOS) (string, bool) {
	optionsDir, err := desktop.ResolveIntelliJOptionsDir(hostOS)
	return optionsDir, err == nil
}

func resolveInstalledIntelliJContentsDir(hostOS model.HostOS) (string, error) {
	if hostOS != model.HostOSDarwin {
		return "", fmt.Errorf("IntelliJ IDEA app discovery is unsupported on %s", hostOS)
	}
	// ~/Applications shadows /Applications.
	homeDir, _ := desktop.IdeUserHomeDir()
	patterns := make([]string, 0, 2)
	if strings.TrimSpace(homeDir) != "" {
		patterns = append(patterns, filepath.Join(homeDir, "Applications", "IntelliJ IDEA*.app", "Contents"))
	}
	patterns = append(patterns, "/Applications/IntelliJ IDEA*.app/Contents")
	for _, pattern := range patterns {
		matches, err := desktop.IdeGlob(pattern)
		if err != nil {
			continue
		}
		slices.Sort(matches)
		for _, match := range matches {
			info, err := desktop.IdeStat(match)
			if err == nil && info.IsDir() {
				return match, nil
			}
		}
	}
	return "", fmt.Errorf("IntelliJ IDEA.app was not found")
}

func emitIntelliJManualOpenGuidance(ctx core.Context, result config.OpenResult) {
	out := ctx.Stderr
	if out == nil {
		out = ctx.Stdout
	}
	if out == nil {
		return
	}
	info := sshconn.SSHConnectionInfoForResult(result)
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
		textfmt.ValueOrNone(info.PrivateKeyPath),
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

func resolveIntelliJGatewayOptionsDir(optionsDir string) (string, error) {
	optionsDir = filepath.Clean(strings.TrimSpace(optionsDir))
	if filepath.Base(optionsDir) != "options" {
		return "", fmt.Errorf("IntelliJ options directory must end with options: %s", optionsDir)
	}
	versionDir := filepath.Base(filepath.Dir(optionsDir))
	if strings.TrimSpace(versionDir) == "" || versionDir == "." || versionDir == string(filepath.Separator) {
		return "", fmt.Errorf("IntelliJ version directory not found for %s", optionsDir)
	}
	homeDir, err := desktop.IdeUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "Library", "Caches", "JetBrains", versionDir, "tmp", "JetBrainsGateway", "config", "options"), nil
}

func ensureIntelliJGatewayConfigDir(ctx core.Context, optionsDir string) (string, error) {
	gatewayOptionsDir, err := resolveIntelliJGatewayOptionsDir(optionsDir)
	if err != nil {
		return "", err
	}
	gatewayConfigDir := filepath.Dir(gatewayOptionsDir)
	ctx.TraceCommand("", "mkdir", "-p", gatewayOptionsDir)
	if !ctx.DryRun {
		if err := ideMkdirAll(gatewayOptionsDir, 0o700); err != nil {
			return "", err
		}
	}
	infoPath := filepath.Join(gatewayConfigDir, "info.xml")
	if _, err := desktop.IdeStat(infoPath); err == nil {
		return gatewayConfigDir, nil
	}
	ctx.TraceCommand("", "write-xml", infoPath)
	if !ctx.DryRun {
		if err := ideWriteFile(infoPath, []byte(intelliJGatewayInfoXML()), 0o600); err != nil {
			return "", err
		}
	}
	return gatewayConfigDir, nil
}

func intelliJGatewayLogDir(optionsDir string) (string, error) {
	versionDir := filepath.Base(filepath.Dir(filepath.Clean(optionsDir)))
	homeDir, err := desktop.IdeUserHomeDir()
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
