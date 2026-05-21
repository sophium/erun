package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// kubeconfigUserHomeDir is the seam tests use to point lookups at a
// fake home directory. Mirrors awsConfigUserHomeDir / openUserHomeDir.
var kubeconfigUserHomeDir = os.UserHomeDir

// kubeconfigUser is the subset of a `users[].user` entry we need to
// recover the bearer token erun's cloud-context restore flow uses to
// re-write kubectl credentials. Only `token` is read here; the
// exec/auth-provider alternatives are not used by erun-managed cloud
// contexts because configureCloudKubeContext writes a literal token.
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

// LookupKubeContextBearerToken reads the active kubeconfig and
// returns the bearer token for the user entry whose name matches the
// supplied context name. erun's `configureCloudKubeContext` writes
// the user entry with name = KubernetesContext, so a healthy host
// always has the token under that exact key. Returns ok=false when
// the file does not exist, the named user is missing, or the user
// is configured via a different auth method (exec, auth-provider,
// etc.) than a static bearer token.
//
// KUBECONFIG is honored: when set, only the first path in the
// list-separated value is read. The doctor only needs ONE bearer
// token; iterating every entry would invite confusion if the user's
// kubeconfig is a merge of multiple files where the same context
// appears in more than one.
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
		// KUBECONFIG can be a list separated by os.PathListSeparator.
		// Take the first entry — see the docstring on
		// LookupKubeContextBearerToken for why "first" is fine.
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
