package eruncommon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// gatewaySecretRequestTimeout bounds one namespace's read. An environment on an
// unreachable cluster must cost the picker a bounded wait, not kubectl's own
// retry budget.
const gatewaySecretRequestTimeout = "10s"

// GatewayCredentialCandidate is a Secret the gateway credential could be read
// from, and where it was found. Only the Secret's name and the names of its
// keys are read — never a value, so listing candidates cannot leak a credential
// into the desktop's read model.
type GatewayCredentialCandidate struct {
	Name string `json:"name"`
	// Namespaces are the environment namespaces this Secret was found in. A
	// catalog is erun-level, so a candidate present in only some of them is
	// worth naming: the name has to exist wherever it is used.
	Namespaces []string `json:"namespaces,omitempty"`
	// Keys are the Secret's key names, so a caller can offer the token's key
	// rather than asking for it.
	Keys []string `json:"keys,omitempty"`
}

// SecretNamespace names one namespace to look in and the Kubernetes context it
// lives on: environments can sit on different clusters, so the context travels
// with the namespace rather than being assumed.
type SecretNamespace struct {
	Namespace string
	Context   string
}

// ListGatewayCredentialCandidates reads the Secret names present in the given
// namespaces, so an operator can pick one that already exists instead of
// inventing a name the cluster does not carry.
//
// A namespace that cannot be read is reported rather than dropped: an operator
// whose Secret exists but whose access is denied must not be told the Secret is
// missing. The second return value carries those, one line each.
func ListGatewayCredentialCandidates(targets []SecretNamespace) ([]GatewayCredentialCandidate, []string) {
	type entry struct {
		namespaces map[string]struct{}
		keys       map[string]struct{}
	}
	byName := make(map[string]*entry)
	var problems []string

	for _, target := range targets {
		namespace := strings.TrimSpace(target.Namespace)
		if namespace == "" {
			continue
		}
		items, err := listSecretsInNamespace(namespace, strings.TrimSpace(target.Context))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for _, item := range items {
			found := byName[item.name]
			if found == nil {
				found = &entry{namespaces: map[string]struct{}{}, keys: map[string]struct{}{}}
				byName[item.name] = found
			}
			found.namespaces[namespace] = struct{}{}
			for key := range item.keys {
				found.keys[key] = struct{}{}
			}
		}
	}

	out := make([]GatewayCredentialCandidate, 0, len(byName))
	for name, found := range byName {
		out = append(out, GatewayCredentialCandidate{
			Name:       name,
			Namespaces: sortedKeys(found.namespaces),
			Keys:       sortedKeys(found.keys),
		})
	}
	// Name order, so a picker presents the same list every time.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

type listedSecret struct {
	name string
	keys map[string]struct{}
}

// listSecretsInNamespace reads one namespace's Secret names and their key names.
// A namespace holding no Secrets is not an error, and neither is one that does
// not exist yet — an environment may be configured before it is deployed.
func listSecretsInNamespace(namespace, context string) ([]listedSecret, error) {
	// Bounded, because this read drives an operator-facing picker: an
	// unreachable API server must fail the namespace rather than leave the
	// dialog waiting on kubectl's own retry budget.
	args := []string{
		"get", "secrets",
		"--namespace", namespace,
		"--output", "json",
		"--request-timeout", gatewaySecretRequestTimeout,
	}
	if context != "" {
		args = append(args, "--context", context)
	}
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		if isKubectlNotFound(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", namespace, kubectlErrorMessage(err, stderr))
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Data       map[string]string `json:"data"`
			StringData map[string]string `json:"stringData"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: parse the Secret list: %w", namespace, err)
	}
	out := make([]listedSecret, 0, len(list.Items))
	for _, item := range list.Items {
		name := strings.TrimSpace(item.Metadata.Name)
		if name == "" {
			continue
		}
		keys := make(map[string]struct{}, len(item.Data)+len(item.StringData))
		for key := range item.Data {
			keys[key] = struct{}{}
		}
		for key := range item.StringData {
			keys[key] = struct{}{}
		}
		out = append(out, listedSecret{name: name, keys: keys})
	}
	return out, nil
}
