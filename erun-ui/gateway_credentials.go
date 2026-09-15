package main

import (
	eruncommon "github.com/sophium/erun/erun-common"
)

// uiGatewayCredentialCandidate is a Secret the gateway credential could come
// from, so the catalog can offer a name that exists instead of asking for one
// to be invented. Only names are carried: the Secret's value is never read.
type uiGatewayCredentialCandidate struct {
	Name string `json:"name"`
	// Namespaces names the environment namespaces it was found in. A catalog is
	// erun-level, so a candidate present in only some of them is worth seeing —
	// the name has to exist wherever it is used.
	Namespaces []string `json:"namespaces,omitempty"`
	// Keys are the Secret's key names, so the token's key can be picked too.
	Keys []string `json:"keys,omitempty"`
}

// uiGatewayCredentialCandidates is the picker's read: what was found, what
// could not be looked at, and how much ground was covered.
type uiGatewayCredentialCandidates struct {
	Candidates []uiGatewayCredentialCandidate `json:"candidates"`
	// Problems names each namespace that could not be read. Reported rather than
	// dropped: a Secret that exists but whose access is denied must not read as
	// a Secret that is missing.
	Problems []string `json:"problems,omitempty"`
	// Namespaces is how many environment namespaces were searched, so an empty
	// result reads as "none found" rather than "nothing was looked at".
	Namespaces int `json:"namespaces"`
}

// LoadGatewayCredentialCandidates lists the Secrets present in this machine's
// environment namespaces, so the catalog offers the names that exist.
//
// The catalog is erun-level but the Secret lives per environment, so the search
// covers every environment's own namespace, on that environment's own context —
// environments can sit on different clusters. Environments that have no pod,
// such as a host environment, have no namespace to read and are skipped.
func (a *App) LoadGatewayCredentialCandidates() (uiGatewayCredentialCandidates, error) {
	result, err := eruncommon.ResolveListResult(a.deps.store, a.deps.findProjectRoot, eruncommon.OpenParams{})
	if err != nil {
		return uiGatewayCredentialCandidates{}, err
	}

	var targets []eruncommon.SecretNamespace
	seen := make(map[string]struct{})
	for _, tenant := range result.Tenants {
		for _, env := range tenant.Environments {
			// A host environment is a directory on this machine with no pod and
			// no namespace at all, so there is nothing to read for it.
			if env.Type == eruncommon.EnvironmentTypeHost {
				continue
			}
			namespace := eruncommon.KubernetesNamespaceName(tenant.Name, env.Name)
			if _, ok := seen[namespace]; ok {
				continue
			}
			seen[namespace] = struct{}{}
			targets = append(targets, eruncommon.SecretNamespace{
				Namespace: namespace,
				Context:   env.KubernetesContext,
			})
		}
	}

	found, problems := eruncommon.ListGatewayCredentialCandidates(targets)
	out := uiGatewayCredentialCandidates{
		Candidates: make([]uiGatewayCredentialCandidate, 0, len(found)),
		Problems:   problems,
		Namespaces: len(targets),
	}
	for _, candidate := range found {
		out.Candidates = append(out.Candidates, uiGatewayCredentialCandidate{
			Name:       candidate.Name,
			Namespaces: candidate.Namespaces,
			Keys:       candidate.Keys,
		})
	}
	return out, nil
}
