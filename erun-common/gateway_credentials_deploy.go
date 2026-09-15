package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// renderGatewayCredentialsSecret is the Secret erun creates in an
// environment's namespace from the erun-level gateway credential.
//
// It is derived from the credential's value, not named by the operator: one
// catalog means one credential, so there is no name to invent and nothing in a
// cluster to keep in sync with the config.
func renderGatewayCredentialsSecret(name, namespace, authToken string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-deploy
type: Opaque
stringData:
  %s: %q
`, name, namespace, GatewaySecretKey, authToken)
}

// applyGatewayCredentialsSecret delivers the erun-level gateway credential into
// the env's namespace, keeping it out of argv, the trace, and helm release
// values.
//
// The value is read from erun's own operator secret store at deploy time — the
// same store the Cloudflare token uses — rather than referenced as a Secret the
// operator provisions in each cluster. That is the point of the catalog being
// erun-level: the credential is one thing on this machine, and this is what
// fans it out to every environment that selects the gateway.
//
// A configured catalog with no stored credential is an error rather than a
// skipped Secret: the chart would render an env var pointing at a Secret that
// does not exist, and the pod would fail at launch with a message about a
// missing Secret instead of one about the missing credential.
func applyGatewayCredentialsSecret(ctx Context, deployInput HelmDeploySpec) error {
	// Resolved the same way the chart values are, so the Secret exists exactly
	// when this environment reads it: an environment that opted out renders no
	// gateway env block, and gets no Secret to go with it.
	gateway := EffectiveGateway(deployInput.Claude, deployInput.OpenRouter)
	if !gateway.Configured() {
		return nil
	}
	args := kubectlApplyStdinArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply gateway credentials secret " + GatewaySecretName + " (token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	authToken, err := gatewayCredentialForDeploy(gateway.AuthTokenRefName())
	if err != nil {
		return err
	}
	manifest := renderGatewayCredentialsSecret(GatewaySecretName, deployInput.Namespace, authToken)
	return applySecretManifest(deployInput.KubernetesContext, deployInput.Namespace, "gateway credentials secret", manifest, args)
}

// gatewayCredentialForDeploy resolves the credential to deliver: the value saved
// under ref in erun's own operator secret store, or — when none is saved — the
// key this machine's Claude Code already authenticates with.
//
// The fallback is what makes a catalog work the moment it is configured. An
// operator running Claude Code against a gateway has its key on this machine
// already, so requiring them to paste it into a second place would be asking for
// something erun can read. A saved value wins over the fallback, so an operator
// who wants a key other than their own saves one; deleting it restores the
// fallback rather than leaving the gateway unauthenticated.
func gatewayCredentialForDeploy(ref string) (string, error) {
	store, err := DefaultCloudSecretStore()
	if err != nil {
		return "", fmt.Errorf("resolve cloud secret store: %w", err)
	}
	return resolveGatewayCredential(store, ref)
}

// resolveGatewayCredential is the decision itself, separated from the store's
// construction so it can be exercised against a store a test owns.
func resolveGatewayCredential(store CloudSecretStore, ref string) (string, error) {
	saved, loadErr := store.LoadCloudSecret(ref)
	switch {
	case loadErr == nil && strings.TrimSpace(saved) != "":
		return saved, nil
	case loadErr != nil && !errors.Is(loadErr, os.ErrNotExist):
		// A store that cannot be read might hold a value that should win, so
		// this stops rather than quietly delivering a different credential than
		// the operator saved. A missing entry is the ordinary "not saved yet"
		// case, which the host key below answers.
		return "", fmt.Errorf("load gateway credential %q: %w", ref, loadErr)
	}
	if hostToken, ok := HostClaudeGatewayCredential(); ok {
		return hostToken, nil
	}
	return "", fmt.Errorf(
		"no gateway credential: nothing is saved under %q and this machine's Claude Code settings carry none",
		ref,
	)
}
