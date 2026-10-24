package eruncommon

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// maintenanceOffGitConfig turns off the background maintenance git detaches
// after commits, fetches and pushes -- for a push to a local bare remote, in
// the receiving repository too. That detached process outlives the command
// and can still be writing objects/pack when t.TempDir's cleanup removes the
// repository, failing an unrelated test with "directory not empty".
const maintenanceOffGitConfig = "[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n[receive]\n\tautogc = false\n"

// disableGitMaintenanceForSuite points every git child of this test binary at
// a global config that includes the invoking user's own global config and
// turns maintenance off. It travels as GIT_CONFIG_GLOBAL because git keeps
// that for the receive-pack of a local remote, where it drops -c and
// GIT_CONFIG_COUNT. A caller who set GIT_CONFIG_GLOBAL keeps their value.
func disableGitMaintenanceForSuite() error {
	if _, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		return nil
	}
	var b strings.Builder
	for _, include := range userGlobalGitConfigs() {
		b.WriteString("[include]\n\tpath = " + include + "\n")
	}
	b.WriteString(maintenanceOffGitConfig)
	sum := sha256.Sum256([]byte(b.String()))
	path := filepath.Join(os.TempDir(), "erun-common-test-gitconfig-"+hex.EncodeToString(sum[:8]))
	if existing, err := os.ReadFile(path); err != nil || string(existing) != b.String() {
		tmp, err := os.CreateTemp(os.TempDir(), "erun-common-test-gitconfig-*")
		if err != nil {
			return err
		}
		if _, err := tmp.WriteString(b.String()); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
	}
	return os.Setenv("GIT_CONFIG_GLOBAL", path)
}

func userGlobalGitConfigs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig")}
}
