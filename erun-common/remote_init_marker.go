package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RemoteInitMarkerFilename is the path (relative to $HOME inside the
// runtime pod) where `erun init --remote` records the intended bootstrap
// outcome. `erun doctor` running inside the same pod reads this file to
// distinguish "init was deliberately run with --no-git" from "init was
// interrupted before the git checkout finished", and to recover the
// repository URL when offering to finish unfinished work.
const RemoteInitMarkerFilename = ".erun/bootstrap.yaml"

// RemoteInitMarker captures the intent of `erun init --remote` so a
// later doctor invocation inside the same runtime pod can detect what
// was supposed to happen and offer to finish it.
type RemoteInitMarker struct {
	Tenant            string `yaml:"tenant"`
	Environment       string `yaml:"environment"`
	ProjectRoot       string `yaml:"project_root"`
	RepositoryURL     string `yaml:"repository_url,omitempty"`
	NoGit             bool   `yaml:"no_git,omitempty"`
	BootstrapComplete bool   `yaml:"bootstrap_complete"`
}

// RemoteInitMarkerPath joins the marker filename onto homeDir. It does
// not check whether the file exists.
func RemoteInitMarkerPath(homeDir string) string {
	return filepath.Join(homeDir, RemoteInitMarkerFilename)
}

// LoadRemoteInitMarker reads the marker file from homeDir. The found
// return value distinguishes "no marker on disk" from a read/parse
// failure; callers in doctor treat the former as "init never ran or was
// interrupted before its first write".
func LoadRemoteInitMarker(homeDir string) (RemoteInitMarker, bool, error) {
	path := RemoteInitMarkerPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RemoteInitMarker{}, false, nil
		}
		return RemoteInitMarker{}, false, err
	}
	var marker RemoteInitMarker
	if err := yaml.Unmarshal(data, &marker); err != nil {
		return RemoteInitMarker{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return marker, true, nil
}

// SaveRemoteInitMarker writes the marker to homeDir. Used by in-runtime
// doctor recovery after a successful finish; the regular init flow
// writes the marker remotely via remoteInitMarkerWriteScript.
func SaveRemoteInitMarker(homeDir string, marker RemoteInitMarker) error {
	path := RemoteInitMarkerPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(&marker)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// remoteInitMarkerWriteScript renders a POSIX-shell snippet that writes
// the marker to $HOME/<RemoteInitMarkerFilename> inside the runtime
// pod. The body is emitted via cat <<'EOF' so YAML special characters
// are preserved verbatim; the marker fields are validated upstream so a
// stray EOF sentinel in user-provided values is not a concern (tenant,
// environment, and repository URLs are all already constrained to a
// narrow character set by parseRemoteRepositorySpec and the bootstrap
// validators).
func remoteInitMarkerWriteScript(marker RemoteInitMarker) string {
	data, err := yaml.Marshal(&marker)
	if err != nil {
		// yaml.Marshal on a fixed struct cannot fail in practice; fall
		// back to a minimal serialization so the script remains valid.
		data = []byte(fmt.Sprintf("bootstrap_complete: %t\n", marker.BootstrapComplete))
	}
	dir := "$HOME/" + filepath.Dir(RemoteInitMarkerFilename)
	target := "$HOME/" + RemoteInitMarkerFilename
	return strings.Join([]string{
		fmt.Sprintf("mkdir -p %s", shellQuote(dir)),
		fmt.Sprintf("cat > %s <<'__ERUN_BOOTSTRAP_MARKER__'", shellQuote(target)),
		strings.TrimRight(string(data), "\n"),
		"__ERUN_BOOTSTRAP_MARKER__",
		fmt.Sprintf("chmod 600 %s", shellQuote(target)),
	}, "\n")
}
