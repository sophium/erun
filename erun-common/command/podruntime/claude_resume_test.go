//go:build unix

package podruntime

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestIsClaudeInvocationMatchesOnlyTheClaudeLink(t *testing.T) {
	for argv0, want := range map[string]bool{
		"claude":                true,
		"/usr/local/bin/claude": true,
		"erun":                  false,
		"/usr/local/bin/erun":   false,
		"claude-real":           false,
		"/opt/claude/bin/erun":  false,
		"":                      false,
	} {
		if got := IsClaudeInvocation(argv0); got != want {
			t.Errorf("IsClaudeInvocation(%q) = %v, want %v", argv0, got, want)
		}
	}
}

func TestClaudeResumeArgsContinuesTheDirectorysConversation(t *testing.T) {
	var checked []string
	withConversation := func(path string) bool {
		checked = append(checked, path)
		return true
	}
	none := func(string) bool { return false }

	if got := claudeResumeArgs(nil, "/home/erun", "/home/erun/git/erun", withConversation); !reflect.DeepEqual(got, []string{"--continue"}) {
		t.Fatalf("bare claude with a conversation: got %q, want --continue", got)
	}
	if want := []string{"/home/erun/.claude/projects/-home-erun-git-erun"}; !reflect.DeepEqual(checked, want) {
		t.Fatalf("checked %q, want %q", checked, want)
	}
	if got := claudeResumeArgs(nil, "/home/erun", "/home/erun/git/erun", none); len(got) != 0 {
		t.Fatalf("bare claude without a conversation: got %q, want no args", got)
	}
	if got := claudeResumeArgs([]string{"--model", "opus", "fix the build"}, "/home/erun", "/w", withConversation); !reflect.DeepEqual(got, []string{"--continue", "--model", "opus", "fix the build"}) {
		t.Fatalf("ordinary args with a conversation: got %q", got)
	}
}

func TestClaudeResumeArgsPassesExplicitModesThrough(t *testing.T) {
	all := []string{
		"--continue", "-c", "--resume", "-r", "--print", "-p",
		"--session-id", "--fork-session", "--from-pr", "--teleport",
		"--help", "-h", "--version", "-v", "--bg", "--background",
		"mcp", "update", "migrate-installer", "setup-token", "config", "doctor",
		"agents", "attach", "stop", "respawn", "logs", "rm",
	}
	keys := make([]string, 0, len(claudePassthroughArgs))
	for key := range claudePassthroughArgs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sorted := append([]string(nil), all...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Fatalf("passthrough set %q, want %q", keys, sorted)
	}
	conversation := func(string) bool { return true }
	for _, arg := range all {
		args := []string{"--model", "opus", arg, "abc123"}
		if got := claudeResumeArgs(args, "/home/erun", "/w", conversation); !reflect.DeepEqual(got, args) {
			t.Errorf("%s: got %q, want it unchanged", arg, got)
		}
	}
	// A background agent's id addresses that agent, never this directory's
	// most recent conversation.
	logs := []string{"logs", "9f2c"}
	if got := claudeResumeArgs(logs, "/home/erun", "/w", conversation); !reflect.DeepEqual(got, logs) {
		t.Fatalf("got %q, want %q", got, logs)
	}
}

// A flag given as --name=value picks its mode as surely as --name value;
// --session-id beside a prepended --continue makes Claude Code refuse to start.
func TestClaudeResumeArgsPassesExplicitModesWithInlineValuesThrough(t *testing.T) {
	conversation := func(string) bool { return true }
	for _, args := range [][]string{
		{"--resume=abc123"},
		{"-r=abc123"},
		{"--session-id=0f8e5b6c-1d2a-4c3b-9e8f-7a6b5c4d3e2f"},
		{"--session-id", "0f8e5b6c-1d2a-4c3b-9e8f-7a6b5c4d3e2f"},
		{"--model", "opus", "--from-pr=42"},
		{"--teleport=session"},
		{"--resume", "abc123", "--fork-session"},
	} {
		if got := claudeResumeArgs(args, "/home/erun", "/w", conversation); !reflect.DeepEqual(got, args) {
			t.Errorf("%q: got %q, want it unchanged", args, got)
		}
	}
	// A prompt that merely contains = is not a flag.
	prompt := []string{"--model", "opus", "set resume=true in the config"}
	if got := claudeResumeArgs(prompt, "/home/erun", "/w", conversation); !reflect.DeepEqual(got, append([]string{"--continue"}, prompt...)) {
		t.Fatalf("prompt with '=': got %q, want --continue prepended", got)
	}
}

// The expected keys are what Claude Code's own function returns for each path
// (string replace of [^a-zA-Z0-9] with '-', and past 200 characters the
// prefix plus the base-36 absolute value of the path's 32-bit string hash).
func TestClaudeProjectKeyMatchesClaudeCode(t *testing.T) {
	long := "/home/erun/git/" + strings.Repeat("a_very.long directory-name/", 9) + "repo"
	longKey := "-home-erun-git-" + strings.Repeat("a-very-long-directory-name-", 6) + "a-very-long-directory-n"
	for _, tc := range []struct{ path, want string }{
		{"/home/erun/git/erun", "-home-erun-git-erun"},
		{"/home/erun/git/my_repo", "-home-erun-git-my-repo"},
		{"/home/erun/git/erun.io", "-home-erun-git-erun-io"},
		{"/home/erun/git/my repo", "-home-erun-git-my-repo"},
		{"/home/erun/git/\u00fcn\u00ef", "-home-erun-git--n-"},
		{"/home/erun/git/\U0001F600x", "-home-erun-git---x"},
		{long, longKey + "-dn9ift"},
		{long + "2", longKey + "-35hga3"},
		{long + "/\u00fc", longKey + "-qowytw"},
	} {
		if got := claudeProjectKey(tc.path); got != tc.want {
			t.Errorf("claudeProjectKey(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestClaudeResumeArgsFindsTheConversationByClaudeCodesKey(t *testing.T) {
	var checked string
	isDir := func(path string) bool { checked = path; return true }
	if got := claudeResumeArgs(nil, "/home/erun", "/home/erun/git/my_repo.io", isDir); !reflect.DeepEqual(got, []string{"--continue"}) {
		t.Fatalf("got %q, want --continue", got)
	}
	if want := "/home/erun/.claude/projects/-home-erun-git-my-repo-io"; checked != want {
		t.Fatalf("checked %q, want %q", checked, want)
	}
}

// Claude Code keys a conversation by the resolved working directory, so a
// shell standing in a symlinked path still resumes it.
func TestRunClaudeKeysTheResolvedWorkingDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "real_repo")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "usr", "local")
	claudeTestPackage(t, prefix, `{"bin": {"claude": "bin/claude.exe"}}`)
	host := claudeTestHost(map[string]string{"NPM_CONFIG_PREFIX": prefix, "HOME": "/home/erun"})
	host.getwd = func() (string, error) { return link, nil }
	host.isDir = func(path string) bool { return path == "/home/erun/.claude/projects/"+claudeProjectKey(target) }
	var gotArgv []string
	host.exec = func(_ string, argv, _ []string) error {
		gotArgv = argv
		return nil
	}
	if err := runClaude(nil, host); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotArgv, []string{"claude", "--continue"}) {
		t.Fatalf("argv %q, want claude --continue", gotArgv)
	}
}

func TestClaudeResumeArgsNoResumeStartsFreshAndIsNotPassedOn(t *testing.T) {
	isDir := func(string) bool { return true }
	got := claudeResumeArgs([]string{"--no-resume", "--model", "opus"}, "/home/erun", "/home/erun/git/erun", isDir)
	if want := []string{"--model", "opus"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("--no-resume: got %q, want %q", got, want)
	}
	if got := claudeResumeArgs([]string{"--bg", "--new"}, "/home/erun", "/home/erun/git/erun", isDir); strings.Join(got, " ") != "--bg --new" {
		t.Fatalf("--bg --new: got %q, want it unchanged", got)
	}
}

// claudeTestPackage lays out an npm global install of Claude Code under
// prefix and returns the binary its package.json declares.
func claudeTestPackage(t *testing.T, prefix, bin string) string {
	t.Helper()
	pkg := filepath.Join(prefix, "lib", "node_modules", "@anthropic-ai", "claude-code")
	writeAssetFile(t, filepath.Join(pkg, "package.json"), bin)
	path := filepath.Join(pkg, "bin", "claude.exe")
	writeAssetFile(t, path, "#!/bin/true\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeTestHost(env map[string]string) claudeHost {
	return claudeHost{
		getenv:     func(key string) string { return env[key] },
		getwd:      func() (string, error) { return "/home/erun/git/erun", nil },
		isDir:      func(string) bool { return false },
		readFile:   os.ReadFile,
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		executable: func() (string, error) { return "/usr/local/bin/erun", nil },
		environ:    func() []string { return []string{"HOME=/home/erun"} },
	}
}

func TestResolveRealClaudeFindsTheNPMPackageBinary(t *testing.T) {
	root := t.TempDir()
	mapped := claudeTestPackage(t, filepath.Join(root, "npm"), `{"bin": {"claude": "bin/claude.exe"}}`)
	host := claudeTestHost(map[string]string{"NPM_CONFIG_PREFIX": filepath.Join(root, "npm")})
	if got, err := resolveRealClaude(host); err != nil || got != mapped {
		t.Fatalf("map bin: got %q, %v, want %q", got, err, mapped)
	}

	// The prefix node runs from, with the bin declared as a single path.
	single := claudeTestPackage(t, filepath.Join(root, "node"), `{"bin": "bin/claude.exe"}`)
	host = claudeTestHost(nil)
	host.lookPath = func(name string) (string, error) {
		return filepath.Join(root, "node", "bin", name), nil
	}
	if got, err := resolveRealClaude(host); err != nil || got != single {
		t.Fatalf("string bin: got %q, %v, want %q", got, err, single)
	}

	override := filepath.Join(root, "custom-claude")
	writeAssetFile(t, override, "#!/bin/true\n")
	if err := os.Chmod(override, 0o755); err != nil {
		t.Fatal(err)
	}
	host = claudeTestHost(map[string]string{claudeRealEnv: override, "NPM_CONFIG_PREFIX": filepath.Join(root, "npm")})
	if got, err := resolveRealClaude(host); err != nil || got != override {
		t.Fatalf("override: got %q, %v, want %q", got, err, override)
	}
}

func TestResolveRealClaudeFailsClearly(t *testing.T) {
	root := t.TempDir()
	host := claudeTestHost(map[string]string{"NPM_CONFIG_PREFIX": filepath.Join(root, "empty")})
	_, err := resolveRealClaude(host)
	if err == nil || !strings.Contains(err.Error(), "Claude Code is not installed") || !strings.Contains(err.Error(), claudeRealEnv) {
		t.Fatalf("missing package: got %v", err)
	}

	host = claudeTestHost(map[string]string{claudeRealEnv: filepath.Join(root, "missing")})
	if _, err := resolveRealClaude(host); err == nil || !strings.Contains(err.Error(), claudeRealEnv+"=") {
		t.Fatalf("missing override: got %v", err)
	}

	// The claude link itself, or anything resolving to erun, would exec in a loop.
	erun := filepath.Join(root, "erun")
	writeAssetFile(t, erun, "#!/bin/true\n")
	if err := os.Chmod(erun, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "claude")
	if err := os.Symlink(erun, link); err != nil {
		t.Fatal(err)
	}
	host = claudeTestHost(map[string]string{claudeRealEnv: link})
	host.executable = func() (string, error) { return erun, nil }
	if _, err := resolveRealClaude(host); !errors.Is(err, errClaudeIsERun) {
		t.Fatalf("override pointing at erun: got %v, want errClaudeIsERun", err)
	}
}

// What erun execs is what idle stop must find: the binary inside the npm
// package, running as claude.
func TestRunClaudeExecsTheRealBinaryIdleStopMatches(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "usr", "local")
	want := claudeTestPackage(t, prefix, `{"bin": {"claude": "bin/claude.exe"}}`)
	host := claudeTestHost(map[string]string{"NPM_CONFIG_PREFIX": prefix, "HOME": "/home/erun"})
	host.isDir = func(path string) bool { return path == "/home/erun/.claude/projects/-home-erun-git-erun" }
	var gotPath string
	var gotArgv, gotEnv []string
	host.exec = func(path string, argv, env []string) error {
		gotPath, gotArgv, gotEnv = path, argv, env
		return nil
	}
	if err := runClaude([]string{"--model", "opus"}, host); err != nil {
		t.Fatal(err)
	}
	if gotPath != want || !reflect.DeepEqual(gotArgv, []string{"claude", "--continue", "--model", "opus"}) || !reflect.DeepEqual(gotEnv, []string{"HOME=/home/erun"}) {
		t.Fatalf("exec(%q, %q, %q)", gotPath, gotArgv, gotEnv)
	}
	if !isRuntimeAIClient(gotPath, gotArgv) {
		t.Fatalf("idle stop does not match the exec'd claude %q %q", gotPath, gotArgv)
	}
}
