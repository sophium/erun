package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// remoteInitMarkerBaseDir is the directory (relative to $HOME) under
// which per-tenant/per-environment bootstrap markers live. `erun init
// --remote` records the intended bootstrap outcome here so `erun
// doctor` running later can distinguish "init was deliberately run
// with --no-git" from "init was interrupted before the git checkout
// finished", and recover the repository URL when offering to finish
// unfinished work. Scoping the path per tenant/env avoids collisions
// when multiple tenants share one $HOME, on a runtime host or a
// developer machine.
const remoteInitMarkerBaseDir = ".erun"

// remoteInitMarkerFilename is the leaf filename of the bootstrap
// marker inside its per-tenant/per-environment directory.
const remoteInitMarkerFilename = "bootstrap.yaml"

// RemoteInitMarker captures the intent of `erun init --remote` so a
// later doctor invocation can detect what was supposed to happen and
// offer to finish it.
type RemoteInitMarker struct {
	Tenant             string `yaml:"tenant"`
	Environment        string `yaml:"environment"`
	ProjectRoot        string `yaml:"project_root"`
	RepositoryURL      string `yaml:"repository_url,omitempty"`
	CodeCommitHost     string `yaml:"codecommit_host,omitempty"`
	CodeCommitSSHKeyID string `yaml:"codecommit_ssh_key_id,omitempty"`
	NoGit              bool   `yaml:"no_git,omitempty"`
	BootstrapComplete  bool   `yaml:"bootstrap_complete"`
}

// RemoteInitMarkerPath returns the absolute marker path for the given
// tenant/environment under homeDir. It does not check whether the file
// exists.
func RemoteInitMarkerPath(homeDir, tenant, environment string) string {
	return filepath.Join(homeDir, remoteInitMarkerBaseDir, tenant, environment, remoteInitMarkerFilename)
}

// LoadRemoteInitMarker reads the marker file for the given
// tenant/environment from homeDir. The found return value
// distinguishes "no marker on disk" from a read/parse failure;
// callers in doctor treat the former as "init never ran or was
// interrupted before its first write".
func LoadRemoteInitMarker(homeDir, tenant, environment string) (RemoteInitMarker, bool, error) {
	path := RemoteInitMarkerPath(homeDir, tenant, environment)
	return readRemoteInitMarker(path)
}

func readRemoteInitMarker(path string) (RemoteInitMarker, bool, error) {
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

// SaveRemoteInitMarker writes the marker to its per-tenant/per-environment
// path under homeDir. Used by in-runtime doctor recovery after a
// successful finish; the regular init flow writes the marker remotely
// via remoteInitMarkerWriteScript. The marker's Tenant and Environment
// fields determine the destination path and must be set.
func SaveRemoteInitMarker(homeDir string, marker RemoteInitMarker) error {
	if marker.Tenant == "" || marker.Environment == "" {
		return fmt.Errorf("save remote init marker: tenant and environment are required")
	}
	path := RemoteInitMarkerPath(homeDir, marker.Tenant, marker.Environment)
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
// the marker to $HOME/<base>/<tenant>/<environment>/<file> inside the
// runtime pod. The body is emitted via cat <<'EOF' so YAML special
// characters are preserved verbatim; the marker fields are validated
// upstream so a stray EOF sentinel in user-provided values is not a
// concern (tenant, environment, and repository URLs are all already
// constrained to a narrow character set by parseRemoteRepositorySpec
// and the bootstrap validators).
func remoteInitMarkerWriteScript(marker RemoteInitMarker) string {
	data, err := yaml.Marshal(&marker)
	if err != nil {
		// yaml.Marshal on a fixed struct cannot fail in practice; fall
		// back to a minimal serialization so the script remains valid.
		data = []byte(fmt.Sprintf("bootstrap_complete: %t\n", marker.BootstrapComplete))
	}
	relativeDir := filepath.Join(remoteInitMarkerBaseDir, marker.Tenant, marker.Environment)
	dir := "$HOME/" + relativeDir
	target := dir + "/" + remoteInitMarkerFilename
	return strings.Join([]string{
		fmt.Sprintf("mkdir -p %s", shellQuote(dir)),
		fmt.Sprintf("cat > %s <<'__ERUN_BOOTSTRAP_MARKER__'", shellQuote(target)),
		strings.TrimRight(string(data), "\n"),
		"__ERUN_BOOTSTRAP_MARKER__",
		fmt.Sprintf("chmod 600 %s", shellQuote(target)),
	}, "\n")
}
