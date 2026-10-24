package remoteinit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/config"
	"gopkg.in/yaml.v3"
)

// RemoteInitMarkerBaseDir roots per-tenant/env markers that let `erun doctor`
// tell a deliberate --no-git init from an interrupted one.
const RemoteInitMarkerBaseDir = ".erun"

const RemoteInitMarkerFilename = "bootstrap.yaml"

// RemoteInitMarker records the intent of `erun init --remote` so doctor can finish it.
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

func RemoteInitMarkerPath(homeDir, tenant, environment string) string {
	return filepath.Join(homeDir, RemoteInitMarkerBaseDir, tenant, environment, RemoteInitMarkerFilename)
}

// LoadRemoteInitMarker reads the marker; found is false only when it is absent.
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

// SaveRemoteInitMarker writes the marker locally; Tenant and Environment must be set.
func SaveRemoteInitMarker(homeDir string, marker RemoteInitMarker) error {
	if marker.Tenant == "" || marker.Environment == "" {
		return fmt.Errorf("save remote init marker: tenant and environment are required")
	}
	path := RemoteInitMarkerPath(homeDir, marker.Tenant, marker.Environment)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing, _ := os.ReadFile(path)
	data, err := config.MarshalConfigPreservingUnknownFields(existing, &marker)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
