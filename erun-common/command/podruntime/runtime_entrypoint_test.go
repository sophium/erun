package podruntime

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"gopkg.in/yaml.v3"
)

// newTestRuntimeBoot is a container whose home and every fixed image/chart path
// live under the test's own temp dir, with exactly the given environment.
func newTestRuntimeBoot(t *testing.T, env map[string]string) runtimeBoot {
	t.Helper()
	root := t.TempDir()
	boot := newRuntimeBoot(RuntimeEntrypointParams{
		Env:      func(key string) string { return env[key] },
		HomeDir:  filepath.Join(root, "home"),
		ERunPath: "erun",
	})
	boot.serviceAccountDir = filepath.Join(root, "serviceaccount")
	boot.registryCredentialPath = filepath.Join(root, "registry-credential", ".dockerconfigjson")
	boot.platformAliasDir = filepath.Join(root, "platform-alias")
	boot.skillsRoot = filepath.Join(root, "skills")
	boot.agentsRoot = filepath.Join(root, "agents")
	boot.releaseDir = filepath.Join(root, "release")
	boot.procRoot = filepath.Join(root, "proc")
	if err := os.MkdirAll(boot.home, 0o755); err != nil {
		t.Fatal(err)
	}
	return boot
}

func readTestYAML(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, into); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
}

func readTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	object := map[string]any{}
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return object
}

func testJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("want a JSON object, got %T: %v", value, value)
	}
	return object
}

func testJSONArray(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("want a JSON array, got %T: %v", value, value)
	}
	return array
}

func TestRuntimeEntrypointCommandSelectsTheMode(t *testing.T) {
	if runtimeEntrypointCommand(nil) != nil || runtimeEntrypointCommand([]string{"devops"}) != nil {
		t.Fatal("no command and `devops` both boot the environment")
	}
	if got := runtimeEntrypointCommand([]string{"bash", "-lc", "true"}); strings.Join(got, " ") != "bash -lc true" {
		t.Fatalf("any other command runs as the foreground process, got %v", got)
	}
}

// A dry run lists what this container would do and changes nothing.
func TestRunRuntimeEntrypointDryRunChangesNothing(t *testing.T) {
	home := t.TempDir()
	ctx, out := runtimeTestContext(true)
	env := map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev", "ERUN_MCP_PORT": "17000"}
	code, err := RunRuntimeEntrypoint(ctx, RuntimeEntrypointParams{Env: func(key string) string { return env[key] }, HomeDir: home})
	if err != nil || code != 0 {
		t.Fatalf("dry run: code=%d err=%v", code, err)
	}
	for _, want := range []string{"erun config: write the erun config for team/dev", "MCP edge: serve the MCP edge", "--metrics-enabled=true", "serve until the container is stopped"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the dry run should list %q, got:\n%s", want, out.String())
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("a dry run must not write into the home directory, found %d entries", len(entries))
	}
}

func TestRuntimeMCPEnabled(t *testing.T) {
	for _, tc := range []struct {
		enabled, port string
		want          bool
	}{
		{"true", "", true},
		{"1", "", true},
		{"false", "17000", false},
		{"off", "17000", false},
		{"", "17000", true},
		{"", "", false},
	} {
		boot := newTestRuntimeBoot(t, map[string]string{"ERUN_MCP_ENABLED": tc.enabled, "ERUN_MCP_PORT": tc.port})
		if got := boot.runtimeMCPEnabled(); got != tc.want {
			t.Errorf("ERUN_MCP_ENABLED=%q ERUN_MCP_PORT=%q: enabled=%v, want %v", tc.enabled, tc.port, got, tc.want)
		}
		if (describeRuntimeMCP(boot) != "") != tc.want {
			t.Errorf("ERUN_MCP_ENABLED=%q ERUN_MCP_PORT=%q: the MCP step must run exactly when the edge is enabled", tc.enabled, tc.port)
		}
	}
}

// emcp parses its flags with Go's flag package; a space-separated bool value
// would stop parsing at the bare "true" and silently drop every later flag,
// including the tenant and environment. Parsing the argv the way emcp does is
// the check, rather than looking for substrings.
func TestRuntimeMCPArgsParseThroughEveryFlag(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev", "ERUN_MCP_PORT": "17000", "ERUN_NAMESPACE": "team-dev"})
	flags := flag.NewFlagSet("emcp", flag.ContinueOnError)
	values := map[string]*string{}
	for _, name := range []string{"host", "port", "path", "metrics-host", "metrics-port", "tenant", "environment", "repo-path", "kubernetes-context", "namespace"} {
		values[name] = flags.String(name, "", "")
	}
	metrics := flags.Bool("metrics-enabled", false, "")
	if err := flags.Parse(boot.runtimeMCPArgs()); err != nil {
		t.Fatal(err)
	}
	if flags.NArg() != 0 {
		t.Fatalf("emcp would stop parsing at %q", flags.Args())
	}
	got := map[string]string{}
	for name, value := range values {
		got[name] = *value
	}
	want := map[string]string{
		"host": "0.0.0.0", "port": "17000", "path": "/mcp", "metrics-host": "0.0.0.0", "metrics-port": "9100",
		"tenant": "team", "environment": "dev", "repo-path": filepath.Join(boot.home, "git", "erun"),
		"kubernetes-context": "in-cluster", "namespace": "team-dev",
	}
	if !*metrics || !reflect.DeepEqual(got, want) {
		t.Fatalf("emcp would serve with the wrong flags: %v", boot.runtimeMCPArgs())
	}
	if line := boot.runtimeMCPStartLine(); line != "starting erun MCP on 0.0.0.0:17000/mcp, metrics on 0.0.0.0:9100 (enabled=true)" {
		t.Fatalf("start line = %q", line)
	}
}

func TestRuntimeCacheTrimSettings(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		max, interval string
		wantMax       int64
		wantInterval  string
	}{
		{"", "", DefaultGoBuildCacheMaxBytes, "30m0s"},
		{"0", "", 0, "30m0s"},
		{"garbage", "60", 0, "1m0s"},
		{"1024", "-1", 1024, "30m0s"},
	} {
		env := map[string]string{"ERUN_GO_BUILD_CACHE_MAX_BYTES": tc.max, "ERUN_CACHE_TRIM_INTERVAL_SECONDS": tc.interval}
		dir, maxBytes, interval := RuntimeGoBuildCacheTrimSettings(func(key string) string { return env[key] }, home)
		if dir != filepath.Join(home, ".cache", "go-build") || maxBytes != tc.wantMax || interval.String() != tc.wantInterval {
			t.Errorf("max=%q interval=%q: got %s %d %s", tc.max, tc.interval, dir, maxBytes, interval)
		}
	}
	// A cap of zero is how a deployment asks for no bound; read as a bound of
	// zero it would clear the cache, so the boot starts no trim at all.
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_GO_BUILD_CACHE_MAX_BYTES": "0"})
	if describeRuntimeCacheTrim(boot) != "" {
		t.Fatal("a cap of zero must start no cache bound")
	}
}

func writeServiceAccount(t *testing.T, boot runtimeBoot) {
	t.Helper()
	for name, body := range map[string]string{"token": "tok", "ca.crt": "ca", "namespace": "team-dev\n"} {
		writeAssetFile(t, filepath.Join(boot.serviceAccountDir, name), body)
	}
}

func TestWriteRuntimeKubeconfigAliasesTheOuterCloudContext(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1", "ERUN_CLOUD_CONTEXT_NAME": "erun-001-eu-west-2"})
	writeServiceAccount(t, boot)
	if err := writeRuntimeKubeconfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	var config struct {
		CurrentContext string `yaml:"current-context"`
		Clusters       []struct {
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Namespace string `yaml:"namespace"`
			} `yaml:"context"`
		} `yaml:"contexts"`
	}
	readTestYAML(t, boot.kubeconfigPath(), &config)
	if config.CurrentContext != "in-cluster" || config.Clusters[0].Cluster.Server != "https://10.43.0.1:443" {
		t.Fatalf("unexpected kubeconfig: %+v", config)
	}
	if len(config.Contexts) != 2 || config.Contexts[1].Name != "erun-001-eu-west-2" || config.Contexts[1].Context.Namespace != "team-dev" {
		t.Fatalf("the outer context should alias the in-cluster one: %+v", config.Contexts)
	}
}

func TestWriteRuntimeKubeconfigNeedsAServiceAccount(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1"})
	if err := writeRuntimeKubeconfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(boot.kubeconfigPath()); !os.IsNotExist(err) {
		t.Fatalf("no mounted service account means nothing to point at: %v", err)
	}
	if describeRuntimeKubeconfig(newTestRuntimeBoot(t, nil)) != "" {
		t.Fatal("outside a cluster the kubeconfig step has nothing to do")
	}
}

func TestNormalizeRuntimeSSHPermissions(t *testing.T) {
	boot := newTestRuntimeBoot(t, nil)
	key := boot.path(".ssh", "id_ed25519")
	writeAssetFile(t, key, "private")
	writeAssetFile(t, key+".pub", "public")
	for _, path := range []string{key, key + ".pub"} {
		if err := os.Chmod(path, 0o660); err != nil {
			t.Fatal(err)
		}
	}
	if err := normalizeRuntimeSSHPermissions(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{key: 0o600, key + ".pub": 0o644, boot.path(".ssh"): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", path, info.Mode().Perm(), want)
		}
	}
}

func TestRuntimeShellHookIsInstalledOnce(t *testing.T) {
	boot := newTestRuntimeBoot(t, nil)
	writeAssetFile(t, boot.path(".bashrc"), "alias ll='ls -l'\n")
	for i := 0; i < 2; i++ {
		if err := installRuntimeShellHook(core.Context{}, boot, nil); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(boot.path(".bashrc"))
	if strings.Count(string(data), runtimeShellHookBegin) != 1 || !strings.HasPrefix(string(data), "alias ll='ls -l'\n") {
		t.Fatalf("the managed block must be replaced, not stacked, and the rest kept:\n%s", data)
	}
	if _, err := os.Stat(boot.path(".bash_profile")); !os.IsNotExist(err) {
		t.Fatal("creating ~/.bash_profile would stop bash reading ~/.profile")
	}
	hook, _ := os.ReadFile(boot.path(".erun-shell-hook.bashrc"))
	if strings.Contains(string(hook), "configure-") {
		t.Fatalf("the hook must not run configure scripts per shell:\n%s", hook)
	}
}

func TestRewriteRuntimeCodexConfigIsIdempotentAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeAssetFile(t, path, "model = \"o3\"\nsandbox_mode = \"read-only\"\n\n[profiles.fast]\nmodel = \"o4\"\n\n[mcp_servers.erun]\nurl = \"http://old\"\n\n[mcp_servers.other]\nurl = \"http://other\"\n")
	if err := rewriteRuntimeCodexConfig(path, "http://127.0.0.1:17000/mcp"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	want := "model = \"o3\"\n\nsandbox_mode = \"danger-full-access\"\napproval_policy = \"on-request\"\n\n[profiles.fast]\nmodel = \"o4\"\n\n[mcp_servers.other]\nurl = \"http://other\"\n\n[mcp_servers.erun]\nurl = \"http://127.0.0.1:17000/mcp\"\ntool_timeout_sec = 600\n"
	if string(first) != want {
		t.Fatalf("config.toml =\n%s\nwant\n%s", first, want)
	}
	if err := rewriteRuntimeCodexConfig(path, "http://127.0.0.1:17000/mcp"); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); string(second) != string(first) {
		t.Fatalf("a reboot must rewrite the same bytes:\n%s", second)
	}
}

func TestWireRuntimeClaudeMCPRegistersTheEdgeForTheProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeAssetFile(t, path, `{"numStartups": 12, "projects": {"/other": {"allowedTools": []}}}`)
	if err := wireRuntimeClaudeMCP(path, "/home/erun/git/erun", "http://127.0.0.1:17000/mcp"); err != nil {
		t.Fatal(err)
	}
	state := readTestJSON(t, path)
	projects := testJSONObject(t, state["projects"])
	server := testJSONObject(t, testJSONObject(t, testJSONObject(t, projects["/home/erun/git/erun"])["mcpServers"])["erun"])
	if server["type"] != "http" || server["url"] != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected server entry: %v", server)
	}
	if state["numStartups"] != float64(12) || projects["/other"] == nil {
		t.Fatalf("the rest of the state file must be kept: %v", state)
	}
}

func TestIsRuntimeAIClientMatchesTheRealBinaries(t *testing.T) {
	for _, tc := range []struct {
		exe  string
		argv []string
		want bool
	}{
		{"/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe", []string{"claude", "--continue"}, true},
		{"/usr/local/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/codex/codex", []string{"codex"}, true},
		{"/usr/local/bin/node", []string{"node", "/usr/local/bin/codex", "exec"}, true},
		{"", []string{"claude"}, true},
		{"/usr/local/bin/erun", []string{"erun", "activity", "ai-session", "hook", "--tool", "claude"}, false},
		{"/usr/bin/bash", []string{"bash", "-c", "claude"}, false},
		{"/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/yarn/bin/yarn.js"}, false},
	} {
		if got := isRuntimeAIClient(tc.exe, tc.argv); got != tc.want {
			t.Errorf("isRuntimeAIClient(%q, %q) = %v, want %v", tc.exe, tc.argv, got, tc.want)
		}
	}
}

func TestRuntimeAIClientPIDsReadsTheProcessTable(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, exe, cmdline string) {
		dir := filepath.Join(proc, pid)
		writeAssetFile(t, filepath.Join(dir, "cmdline"), cmdline)
		if exe != "" {
			if err := os.Symlink(exe, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("101", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe", "claude\x00--continue\x00")
	write("102", "/usr/local/bin/erun", "erun\x00activity\x00sample\x00")
	write("103", "", "") // a zombie has no command line
	write("self", "", "claude\x00")
	pids := runtimeAIClientPIDs(proc)
	if len(pids) != 1 || pids[0] != 101 {
		t.Fatalf("got %v, want only the claude process", pids)
	}
}

func TestRuntimePIDFileRunningChecksTheCommandLine(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "sshd.pid")
	writeAssetFile(t, pidFile, "42\n")
	if runtimePIDFileRunning(root, pidFile, "sshd") {
		t.Fatal("a pid with no live process is not running")
	}
	// A recycled pid: a previous pod's pid now names another program.
	writeAssetFile(t, filepath.Join(root, "42", "cmdline"), "erun\x00activity\x00ssh-proxy\x00")
	if runtimePIDFileRunning(root, pidFile, "sshd") {
		t.Fatal("a recycled pid running another program must not count")
	}
	if !runtimePIDFileRunning(root, pidFile, "ssh-proxy") {
		t.Fatal("the expected program on the recorded pid is running")
	}
}

// The runtime image baked /etc/erun/agents with --chmod=0644, which COPY applies
// to the directory as well: a directory without its execute bit cannot be
// entered, so every agent file in it failed to open and the boot installed no
// agent at all. Both baked trees must stay enterable. Reads a sibling module, so
// it runs uncached (go test -count=1) in the image's cli-test stage.
func TestRuntimeImageBakesAgentAssetsIntoEnterableDirectories(t *testing.T) {
	path := filepath.Join(repoRootForDockerignoreTest(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"COPY --chmod=0755 erun-skills/skills " + core.RuntimeBakedSkillsDir,
		"COPY --chmod=0755 erun-skills/agents " + RuntimeBakedAgentsDir,
	} {
		if !strings.Contains(string(data), line+"\n") {
			t.Errorf("%s must bake with %q: a baked directory without its execute bit installs nothing", path, line)
		}
	}
}
