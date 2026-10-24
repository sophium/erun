package remoteinit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"gopkg.in/yaml.v3"
)

// RemoteInitMarkerBaseDir roots per-tenant/env markers that let `erun doctor`
// tell a deliberate --no-git init from an interrupted one.
const RemoteInitMarkerBaseDir = ".erun"

const RemoteInitMarkerFilename = "bootstrap.yaml"

func RemoteInitMarkerPath(homeDir, tenant, environment string) string {
	return filepath.Join(homeDir, RemoteInitMarkerBaseDir, tenant, environment, RemoteInitMarkerFilename)
}

// LoadRemoteInitMarker reads the marker; found is false only when it is absent.
func LoadRemoteInitMarker(homeDir, tenant, environment string) (model.RemoteInitMarker, bool, error) {
	path := RemoteInitMarkerPath(homeDir, tenant, environment)
	return readRemoteInitMarker(path)
}

func readRemoteInitMarker(path string) (model.RemoteInitMarker, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.RemoteInitMarker{}, false, nil
		}
		return model.RemoteInitMarker{}, false, err
	}
	var marker model.RemoteInitMarker
	if err := yaml.Unmarshal(data, &marker); err != nil {
		return model.RemoteInitMarker{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return marker, true, nil
}

// SaveRemoteInitMarker writes the marker locally; Tenant and Environment must be set.
func SaveRemoteInitMarker(homeDir string, marker model.RemoteInitMarker) error {
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
