package podruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ClaudeProgramName is the name the runtime image links to erun.
const ClaudeProgramName = "claude"

const claudeRealEnv = "ERUN_CLAUDE_REAL"

const claudeCodePackage = "@anthropic-ai/claude-code"

// claudePassthroughArgs pick their own conversation, so --continue must not be prepended.
var claudePassthroughArgs = map[string]bool{
	"--continue": true, "-c": true, "--resume": true, "-r": true,
	"--session-id": true, "--fork-session": true, "--from-pr": true, "--teleport": true,
	"--print": true, "-p": true,
	"--help": true, "-h": true, "--version": true, "-v": true,
	"--bg": true, "--background": true,
	"mcp": true, "update": true, "migrate-installer": true, "setup-token": true,
	"config": true, "doctor": true,
	"agents": true, "attach": true, "stop": true, "respawn": true, "logs": true, "rm": true,
}

// IsClaudeInvocation reports whether erun was started through the claude link.
func IsClaudeInvocation(argv0 string) bool {
	return filepath.Base(argv0) == ClaudeProgramName
}

type claudeHost struct {
	getenv     func(string) string
	getwd      func() (string, error)
	isDir      func(string) bool
	readFile   func(string) ([]byte, error)
	lookPath   func(string) (string, error)
	executable func() (string, error)
	exec       func(argv0 string, argv []string, envv []string) error
	environ    func() []string
}

// RunClaude execs the real Claude Code, resuming the directory's latest conversation by default.
func RunClaude(args []string) error {
	host, err := defaultClaudeHost()
	if err != nil {
		return err
	}
	return runClaude(args, host)
}

func runClaude(args []string, host claudeHost) error {
	binary, err := resolveRealClaude(host)
	if err != nil {
		return err
	}
	cwd, err := host.getwd()
	if err != nil {
		return fmt.Errorf("claude: cannot read the working directory: %w", err)
	}
	// Claude Code keys conversations by the resolved working directory.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	argv := append([]string{ClaudeProgramName}, claudeResumeArgs(args, host.getenv("HOME"), cwd, host.isDir)...)
	return host.exec(binary, argv, host.environ())
}

// claudeNoResumeFlag is erun's own flag and is stripped before Claude Code sees it.
const claudeNoResumeFlag = "--no-resume"

func claudeResumeArgs(args []string, home, cwd string, isDir func(string) bool) []string {
	if kept := withoutArg(args, claudeNoResumeFlag); len(kept) != len(args) {
		return kept
	}
	for _, arg := range args {
		name := arg
		if strings.HasPrefix(arg, "-") {
			name, _, _ = strings.Cut(arg, "=")
		}
		if claudePassthroughArgs[name] {
			return args
		}
	}
	if isDir(home + "/.claude/projects/" + claudeProjectKey(cwd)) {
		return append([]string{"--continue"}, args...)
	}
	return args
}

func withoutArg(args []string, drop string) []string {
	kept := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != drop {
			kept = append(kept, arg)
		}
	}
	return kept
}

const claudeProjectKeyLimit = 200

// claudeProjectKey reproduces Claude Code's ~/.claude/projects directory naming:
// non-alphanumeric UTF-16 units become '-', long keys get a base-36 Java string hash suffix.
func claudeProjectKey(cwd string) string {
	var key strings.Builder
	var hash int32
	for _, unit := range utf16.Encode([]rune(cwd)) {
		hash = hash*31 + int32(unit)
		if unit < 0x80 && isASCIIAlnum(byte(unit)) {
			key.WriteByte(byte(unit))
		} else {
			key.WriteByte('-')
		}
	}
	if key.Len() <= claudeProjectKeyLimit {
		return key.String()
	}
	magnitude := int64(hash)
	if magnitude < 0 {
		magnitude = -magnitude
	}
	return key.String()[:claudeProjectKeyLimit] + "-" + strconv.FormatInt(magnitude, 36)
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// resolveRealClaude never resolves to erun itself, which would exec in a loop.
func resolveRealClaude(host claudeHost) (string, error) {
	if path := host.getenv(claudeRealEnv); path != "" {
		if err := checkRealClaude(host, path); err != nil {
			return "", fmt.Errorf("claude: %s=%s: %w", claudeRealEnv, path, err)
		}
		return path, nil
	}
	var tried []string
	for _, prefix := range claudeNPMPrefixes(host) {
		pkgDir := filepath.Join(prefix, "lib", "node_modules", claudeCodePackage)
		bin, ok := claudePackageBin(host, pkgDir)
		if !ok {
			tried = append(tried, pkgDir)
			continue
		}
		path := filepath.Join(pkgDir, bin)
		if err := checkRealClaude(host, path); err != nil {
			tried = append(tried, path+" ("+err.Error()+")")
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("claude: Claude Code is not installed (looked in %s); install %s globally with npm or set %s to its binary", strings.Join(tried, ", "), claudeCodePackage, claudeRealEnv)
}

func claudeNPMPrefixes(host claudeHost) []string {
	var prefixes []string
	add := func(prefix string) {
		for _, seen := range prefixes {
			if seen == prefix {
				return
			}
		}
		prefixes = append(prefixes, prefix)
	}
	if prefix := host.getenv("NPM_CONFIG_PREFIX"); prefix != "" {
		add(prefix)
	}
	if node, err := host.lookPath("node"); err == nil {
		add(filepath.Dir(filepath.Dir(node)))
	}
	add("/usr/local")
	return prefixes
}

// The package bin field is either a path or a map of command names to paths.
func claudePackageBin(host claudeHost, pkgDir string) (string, bool) {
	data, err := host.readFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return "", false
	}
	var manifest struct {
		Bin json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return "", false
	}
	var single string
	if json.Unmarshal(manifest.Bin, &single) == nil && single != "" {
		return single, true
	}
	var named map[string]string
	if json.Unmarshal(manifest.Bin, &named) == nil && named[ClaudeProgramName] != "" {
		return named[ClaudeProgramName], true
	}
	return "", false
}

var errClaudeIsERun = errors.New("it is erun itself, not Claude Code")

func checkRealClaude(host claudeHost, path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("not an executable file")
	}
	if self, err := host.executable(); err == nil {
		if selfResolved, err := filepath.EvalSymlinks(self); err == nil && selfResolved == resolved {
			return errClaudeIsERun
		}
	}
	return nil
}
