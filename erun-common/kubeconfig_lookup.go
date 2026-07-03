package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var kubeconfigUserHomeDir = os.UserHomeDir

// erun-managed cloud contexts always store a literal bearer token, so only
// that field matters here; the exec/auth-provider variants never appear.
type kubeconfigUser struct {
	Token string `yaml:"token,omitempty"`
}

type kubeconfigUserEntry struct {
	Name string         `yaml:"name"`
	User kubeconfigUser `yaml:"user"`
}

type kubeconfigDocument struct {
	Users []kubeconfigUserEntry `yaml:"users"`
}

// LookupKubeContextBearerToken returns the static bearer token stored for the
// given context name, with ok=false when there is none (missing file, missing
// user, or a non-static auth method). Lookup matches on context name because
// configureCloudKubeContext keys each user entry by its context name. Only the
// first KUBECONFIG path is read: a single token suffices, and a merged
// kubeconfig can list the same context in more than one file.
func LookupKubeContextBearerToken(contextName string) (string, bool, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return "", false, errors.New("context name is required")
	}
	path, err := kubeconfigPath()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	doc := kubeconfigDocument{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false, err
	}
	for _, entry := range doc.Users {
		if strings.TrimSpace(entry.Name) != contextName {
			continue
		}
		token := strings.TrimSpace(entry.User.Token)
		if token == "" {
			return "", false, nil
		}
		return token, true, nil
	}
	return "", false, nil
}

func kubeconfigPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("KUBECONFIG")); override != "" {
		if idx := strings.IndexByte(override, os.PathListSeparator); idx > 0 {
			override = override[:idx]
		}
		return override, nil
	}
	home, err := kubeconfigUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}
