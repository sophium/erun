package main

import "strings"

// orchestratorHookCommandIsPortable reports whether a hook command is exactly
// one "node -e '<script>'" invocation with no stray single quote inside the
// script that would close the argument early. Every orchestrator hook is
// emitted in this shape rather than as a POSIX shell one-liner, because a
// POSIX-only construct ("[ ... ]" test syntax, "$(...)" command substitution,
// sh's "&&"/"||" chaining) parses as something else entirely the moment the
// host's own hook shell is not sh -- on Windows the harness runs orchestrator
// hooks through PowerShell, where a leading "[" opens a type literal instead
// of executing. A single quoted "node -e" argument is inert to the invoking
// shell regardless of which one it is, so this check does not vary by host
// OS: the same command is what every orchestrator installs on every
// platform, which is the fix.
func orchestratorHookCommandIsPortable(command string) bool {
	const prefix = `node -e '`
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, `'`) {
		return false
	}
	script := command[len(prefix) : len(command)-1]
	return !strings.Contains(script, `'`)
}
