package secretstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
)

// FileCloudSecretStore hashes the ref so alias punctuation never reaches the path.
type FileCloudSecretStore struct {
	dir string
}

// NewFileCloudSecretStore stores secrets in 0600 files under dir.
func NewFileCloudSecretStore(dir string) cloudstore.CloudSecretStore {
	return FileCloudSecretStore{dir: strings.TrimSpace(dir)}
}

const CloudSecretStoreDirName = "cloud-secrets"

// DefaultCloudSecretStore is rooted under the erun config dir.
func DefaultCloudSecretStore() (cloudstore.CloudSecretStore, error) {
	dir, err := config.ERunConfigDir()
	if err != nil {
		return nil, err
	}
	return NewFileCloudSecretStore(filepath.Join(dir, CloudSecretStoreDirName)), nil
}

// CloudSecretFileName lets pod provisioning name the file the store will later read.
func CloudSecretFileName(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:]) + ".token"
}

func (s FileCloudSecretStore) Path(ref string) string {
	return filepath.Join(s.dir, CloudSecretFileName(ref))
}

func (s FileCloudSecretStore) SaveCloudSecret(ref, value string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("secret ref is required")
	}
	if s.dir == "" {
		return fmt.Errorf("cloud secret store directory is not configured")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create cloud secret store directory: %w", err)
	}
	if err := os.WriteFile(s.Path(ref), []byte(value), 0o600); err != nil {
		return fmt.Errorf("write cloud secret: %w", err)
	}
	return nil
}

func (s FileCloudSecretStore) LoadCloudSecret(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("secret ref is required")
	}
	data, err := os.ReadFile(s.Path(ref))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s FileCloudSecretStore) DeleteCloudSecret(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	if err := os.Remove(s.Path(ref)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete cloud secret: %w", err)
	}
	return nil
}
