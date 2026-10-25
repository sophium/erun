package orchestrator

import "strings"

// orchestratorHookCommandIsPortable reports whether command is exactly one
// "node -e '<script>'" call. Windows runs hooks through PowerShell, where POSIX
// syntax misparses; a single-quoted node argument is inert to any shell.
func orchestratorHookCommandIsPortable(command string) bool {
	const prefix = `node -e '`
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, `'`) {
		return false
	}
	script := command[len(prefix) : len(command)-1]
	return !strings.Contains(script, `'`)
}
