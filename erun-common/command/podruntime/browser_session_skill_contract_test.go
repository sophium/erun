package podruntime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	storageStateToPathPattern = regexp.MustCompile(`storageState\(\s*\{[^}]*\bpath\s*:`)
	ownerOnlySessionWrite     = regexp.MustCompile(`writeFileSync\([^\n]*\{\s*mode:\s*0o600\s*\}\)`)
	ownerOnlySessionChmod     = regexp.MustCompile(`chmodSync\([^,]+,\s*0o600\)`)
)

// The saved session is a live login: Playwright's storageState({ path }) would
// write it under the process umask, readable by every local user.
func TestBrowserSessionSkillWritesSessionsOwnerOnly(t *testing.T) {
	dir := filepath.Join(erunRepoRoot(t), "erun-skills", "skills", "erun-browser-session-rest")
	for _, name := range []string{"save-session.mjs", "request.mjs"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		source := string(data)
		if storageStateToPathPattern.MatchString(source) {
			t.Errorf("%s hands the session path to storageState, which writes it with the umask's permissions", name)
		}
		if !strings.Contains(source, "writeSession(sessionPath, await context.storageState())") {
			t.Errorf("%s no longer saves the session through writeSession", name)
		}
		if !ownerOnlySessionWrite.MatchString(source) || !ownerOnlySessionChmod.MatchString(source) {
			t.Errorf("%s must create the session file with mode 0o600 and tighten an existing one to 0o600 before writing", name)
		}
	}
}
