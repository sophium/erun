package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// remoteInitMarkerBaseDir roots the per-tenant/env bootstrap markers that let a
// later `erun doctor` tell a deliberate --no-git init from one interrupted before
// the git checkout finished, and recover the repository URL to finish that work.
// Per-tenant/env scoping avoids marker collisions when tenants share one $HOME.
const remoteInitMarkerBaseDir = ".erun"

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

// RemoteInitMarkerPath returns the marker path for the given tenant/environment under homeDir.
func RemoteInitMarkerPath(homeDir, tenant, environment string) string {
	return filepath.Join(homeDir, remoteInitMarkerBaseDir, tenant, environment, remoteInitMarkerFilename)
}

// LoadRemoteInitMarker reads the tenant/environment marker under homeDir. The found
// flag distinguishes an absent marker — init never ran, or was interrupted before its
// first write — from a read/parse failure.
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

// SaveRemoteInitMarker persists the marker locally, as in-runtime doctor recovery does
// after finishing an interrupted init; the normal init flow writes it remotely instead.
// Tenant and Environment must be set.
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

// remoteInitMarkerWriteScript renders the shell snippet that writes the marker inside
// the runtime pod. No heredoc escaping is needed: tenant, environment, and repository
// values are constrained to a narrow character set upstream (parseRemoteRepositorySpec
// and the bootstrap validators), so none can forge the closing sentinel.
func remoteInitMarkerWriteScript(marker RemoteInitMarker) string {
	data, err := yaml.Marshal(&marker)
	if err != nil {
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
