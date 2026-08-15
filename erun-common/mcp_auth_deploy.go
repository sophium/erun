package eruncommon

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// resolveMCPAuthPublicKey loads the desktop key the per-env MCP edge verifies
// against. A blank path is the default: no MCP auth, so the edge stays
// loopback-only (ok is false).
func resolveMCPAuthPublicKey(path string) (string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read mcp auth public key %s: %w", path, err)
	}
	return string(pem), true, nil
}

// applyMCPAuthToRuntimeSpec applies MCP-auth metadata only to the runtime
// release: the MCP edge is served by the runtime container, so
// backend component charts never carry mcpAuth. Auth is sticky — a redeploy that
// does not re-supply the key rethreads the env's persisted one — and turning it
// off is explicit, so a routine version bump can never quietly downgrade a
// publicly-reachable edge to unauthenticated.
func applyMCPAuthToRuntimeSpec(ctx Context, target DeployTarget, spec *DeploySpec) error {
	if spec == nil || spec.Deploy.ReleaseName != RuntimeReleaseName(target.Tenant) {
		return nil
	}
	keyPath := resolveMCPAuthKeyPathForDeploy(ctx, target, spec.Target.EnvConfig)
	pem, ok, err := resolveMCPAuthPublicKey(keyPath)
	if err != nil {
		return err
	}
	if ok {
		// Desktop path: a local public key → trust a file:// issuer.
		applyMCPAuthDeployMetadata(&spec.Deploy, pem, keyPath)
		return nil
	}
	if !target.DisableMCPAuth {
		// Hosted path: trust the tenant's registered OIDC issuer (https://). No
		// local key — the MCP edge fetches the issuer's JWKS. Mutually exclusive
		// with the desktop key above; empty issuer leaves the deploy
		// unauthenticated.
		applyMCPAuthOIDCMetadata(&spec.Deploy, spec.Target.EnvConfig.MCPAuthIssuer)
	}
	return nil
}

// resolveMCPAuthKeyPathForDeploy picks the desktop public key this deploy trusts.
// Precedence: the explicit opt-out clears it, then the caller-supplied path, then
// the path the env recorded when auth was last enabled. Rethreading the recorded
// path is what makes auth survive a plain version bump.
func resolveMCPAuthKeyPathForDeploy(ctx Context, target DeployTarget, envConfig EnvConfig) string {
	if target.DisableMCPAuth {
		ctx.Trace("deploy: mcp auth disabled by request; the env's MCP edge will accept loopback traffic only")
		return ""
	}
	if path := strings.TrimSpace(target.MCPAuthPublicKeyPath); path != "" {
		return path
	}
	if path := strings.TrimSpace(envConfig.MCPAuthPublicKeyPath); path != "" {
		ctx.Trace("deploy: mcp auth: rethreading the env's recorded public key " + path)
		return path
	}
	return ""
}

// recordMCPAuthKeyOnEnv records the desktop key on the env at the moment the
// deploy hands it to the cluster, rather than once the whole rollout has
// succeeded. A rollout that fails after helm applied the key leaves a release
// trusting a key the env cannot name, and the next deploy's downgrade refusal
// then has nothing to rethread — recovering the environment needs the very
// record the failure withheld. The OIDC arm carries no local key, so its empty
// field is legitimate and nothing is written there.
func recordMCPAuthKeyOnEnv(ctx Context, deployInput HelmDeploySpec) error {
	keyPath := strings.TrimSpace(deployInput.MCPAuthPublicKeyPath)
	tenant := strings.TrimSpace(deployInput.Tenant)
	environment := strings.TrimSpace(deployInput.Environment)
	if !deployInput.MCPAuthEnabled || keyPath == "" || tenant == "" || environment == "" {
		return nil
	}
	// The read runs in both modes so a dry run traces the write only where a real
	// one would perform it: an env already naming this key needs nothing.
	envConfig, _, err := LoadEnvConfig(tenant, environment)
	if err == nil && strings.TrimSpace(envConfig.MCPAuthPublicKeyPath) == keyPath {
		return nil
	}
	ctx.Trace("deploy: mcp auth: recording the public key " + keyPath + " on " + tenant + "/" + environment)
	if ctx.DryRun {
		return nil
	}
	if err != nil {
		// init writes the env config itself around its own first rollout, so an
		// unreadable one is not worth failing a deploy that did apply the key.
		ctx.Trace("deploy: mcp auth: could not read the env config of " + tenant + "/" + environment + " to record the public key")
		return nil
	}
	envConfig.MCPAuthPublicKeyPath = keyPath
	if err := SaveEnvConfig(tenant, envConfig); err != nil {
		return fmt.Errorf("record mcp auth public key on %s/%s: %w", tenant, environment, err)
	}
	return nil
}

// guardMCPAuthDowngrade refuses a deploy that would strip authentication from an
// env whose live release still has it enabled. The env's MCP edge executes
// arbitrary commands in the pod, so an unnoticed downgrade is the failure mode
// worth a hard stop; the operator opts out explicitly instead.
//
// The live read is the only way to catch an env that enabled auth before the
// setting was recorded in its config, and it only runs on the risky path (a
// runtime deploy whose resolved plan has auth off), so an auth-enabled deploy
// pays nothing. Scoped to explicit deploy requests: `erun open`'s heal-redeploy
// rethreads a recorded key but must not be blocked from handing over a shell.
func guardMCPAuthDowngrade(ctx Context, target DeployTarget, deployInput HelmDeploySpec) error {
	if target.DisableMCPAuth || deployInput.MCPAuthEnabled {
		return nil
	}
	if deployInput.ReleaseName != RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	live, known := readLiveMCPAuth(deployInput)
	if !known {
		return nil
	}
	if !live.enabled {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has none; leaving the MCP edge loopback-only")
		return nil
	}
	ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has it enabled but this deploy resolved none; refusing to downgrade")
	return fmt.Errorf("deploy %s/%s: MCP auth is enabled on the live %s release, but this deploy resolved none — it would leave the environment's MCP edge unauthenticated. %s",
		deployInput.Tenant, deployInput.Environment, deployInput.ReleaseName, mcpAuthDowngradeRecovery(ctx, deployInput, live))
}

// mcpAuthDowngradeRecovery names what the live release actually trusts, so the
// refusal points at the fix instead of asking the operator to identify a key by
// hand — the release's own values and Secret hold the answer. The two arms are
// not interchangeable: an OIDC-authenticated edge has no local key at all, so
// asking it for one sends the operator after a file that should not exist.
func mcpAuthDowngradeRecovery(ctx Context, deployInput HelmDeploySpec, live liveMCPAuth) string {
	const optOut = ", or pass --no-mcp-auth to turn authentication off on purpose"
	if issuer := strings.TrimSpace(live.issuer); issuer != "" && !strings.HasPrefix(issuer, fileIssuerScheme) {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " authenticates against the OIDC issuer " + issuer + "; no local key is involved")
		return "The release authenticates against the OIDC issuer " + issuer + ", not a local key, so restore the env's mcpauthissuer instead of supplying --mcp-auth-public-key" + optOut
	}
	secret := strings.TrimSpace(live.secretName)
	if secret == "" {
		secret = mcpAuthSecretName(deployInput.ReleaseName)
	}
	trusted, ok := readLiveMCPAuthPublicKey(ctx, deployInput, secret)
	if !ok {
		return "The release trusts a desktop public key held in secret " + secret + ", which could not be read from here; re-supply that key with --mcp-auth-public-key <path>" + optOut
	}
	fingerprint := publicKeyFingerprint(trusted)
	if path, matches := desktopIdentityPublicKeyMatching(trusted); matches {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " trusts this host's desktop identity key " + path)
		return "The release trusts the desktop identity key on this host, " + path + " (sha256 " + fingerprint + "); re-supply it with --mcp-auth-public-key " + path + optOut
	}
	return "The release trusts the desktop public key in secret " + secret + " (sha256 " + fingerprint + "), which is not this host's desktop identity key; re-supply the matching key with --mcp-auth-public-key <path>" + optOut
}

// readLiveMCPAuthPublicKey fetches the public key the release's Secret carries,
// so the refusal can identify it rather than describe it. ok=false whenever the
// read or the decode fails; the message then names the Secret alone instead of
// leaking a cluster error into an operator-facing sentence.
func readLiveMCPAuthPublicKey(ctx Context, deployInput HelmDeploySpec, secret string) ([]byte, bool) {
	namespace := strings.TrimSpace(deployInput.Namespace)
	if secret == "" || namespace == "" {
		return nil, false
	}
	ctx.Trace("deploy: mcp auth: reading the key release " + deployInput.ReleaseName + " trusts from secret " + secret)
	args := []string{}
	if kubernetesContext := strings.TrimSpace(deployInput.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--context", kubernetesContext)
	}
	jsonPath := "jsonpath={.data." + strings.ReplaceAll(desktopMCPPublicKeyFile, ".", `\.`) + "}"
	args = append(args, "-n", namespace, "get", "secret", secret, "-o", jsonPath)
	output, err := Command("kubectl", args...).Output()
	if err != nil {
		ctx.Trace("deploy: mcp auth: secret " + secret + " could not be read")
		return nil, false
	}
	pem, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil || len(bytes.TrimSpace(pem)) == 0 {
		ctx.Trace("deploy: mcp auth: secret " + secret + " holds no readable public key")
		return nil, false
	}
	return pem, true
}

// desktopIdentityPublicKeyMatching reports the host path of the desktop identity
// key when it is the very key the release trusts. Naming that path turns the
// refusal into the command that fixes it, and the match is what says re-supplying
// keeps the edge's existing trust rather than rotating it.
func desktopIdentityPublicKeyMatching(trusted []byte) (string, bool) {
	dir := strings.TrimSpace(DefaultDesktopIdentityDir())
	if dir == "" {
		return "", false
	}
	path := DesktopIdentityPublicKeyPath(dir)
	local, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if !bytes.Equal(bytes.TrimSpace(local), bytes.TrimSpace(trusted)) {
		return "", false
	}
	return path, true
}

func publicKeyFingerprint(pem []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(pem))
	return hex.EncodeToString(sum[:])
}

// mcpAuthLiveProbeOverrideEnv is a test-only seam that answers what the live
// release says about its MCP edge from a static value instead of reading helm, so
// integration goldens never depend on a real cluster's releases. Not a production
// knob: when the variable is unset the probe performs the real helm read.
// Accepted values: "disabled", or "enabled" optionally followed by the issuer and
// the key Secret as `enabled|<issuer>|<secret>`; anything else (including empty)
// means unknown, which is how a machine with no reachable release answers.
const mcpAuthLiveProbeOverrideEnv = "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE"

// liveMCPAuth is what the deployed release says about its own MCP edge: whether
// it authenticates, the issuer it trusts, and the Secret carrying the desktop
// key. Enough to name the key a refused deploy would otherwise have dropped.
type liveMCPAuth struct {
	enabled    bool
	issuer     string
	secretName string
}

// readLiveMCPAuth reads the deployed release's MCP-auth settings. known=false
// means the release could not be read (absent, no helm, no cluster) — the caller
// then imposes no constraint rather than block a deploy on an unreachable
// cluster.
func readLiveMCPAuth(deployInput HelmDeploySpec) (liveMCPAuth, bool) {
	if override, ok := os.LookupEnv(mcpAuthLiveProbeOverrideEnv); ok {
		return parseLiveMCPAuthOverride(override)
	}
	release := strings.TrimSpace(deployInput.ReleaseName)
	namespace := strings.TrimSpace(deployInput.Namespace)
	if release == "" || namespace == "" {
		return liveMCPAuth{}, false
	}
	args := []string{"get", "values", release, "--namespace", namespace, "-o", "json"}
	if kubernetesContext := strings.TrimSpace(deployInput.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--kube-context", kubernetesContext)
	}
	output, err := Command("helm", args...).Output()
	if err != nil {
		return liveMCPAuth{}, false
	}
	var values struct {
		MCPAuth struct {
			Enabled    bool   `json:"enabled"`
			Issuer     string `json:"issuer"`
			SecretName string `json:"secretName"`
		} `json:"mcpAuth"`
	}
	if err := json.Unmarshal(output, &values); err != nil {
		return liveMCPAuth{}, false
	}
	return liveMCPAuth{
		enabled:    values.MCPAuth.Enabled,
		issuer:     strings.TrimSpace(values.MCPAuth.Issuer),
		secretName: strings.TrimSpace(values.MCPAuth.SecretName),
	}, true
}

func parseLiveMCPAuthOverride(override string) (liveMCPAuth, bool) {
	fields := strings.Split(strings.TrimSpace(override), "|")
	switch strings.TrimSpace(fields[0]) {
	case "enabled":
		live := liveMCPAuth{enabled: true}
		if len(fields) > 1 {
			live.issuer = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			live.secretName = strings.TrimSpace(fields[2])
		}
		return live, true
	case "disabled":
		return liveMCPAuth{}, true
	default:
		return liveMCPAuth{}, false
	}
}

// applyMCPAuthOIDCMetadata configures the runtime MCP edge to trust the tenant's
// registered OIDC issuer, with the per-env audience from the shared convention.
// No Secret is applied (the edge verifies against the issuer's published JWKS),
// which is why the chart mounts the desktop key only when a secretName is set.
// A blank issuer is a no-op, leaving a non-desktop deploy unauthenticated.
func applyMCPAuthOIDCMetadata(deployInput *HelmDeploySpec, issuer string) {
	issuer = strings.TrimSpace(issuer)
	if deployInput == nil || issuer == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthIssuer = issuer
	deployInput.MCPAuthAudience = MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
}

// mcpAuthSecretName derives the per-release Secret that carries the desktop
// public key the env's erun-mcp edge verifies bearer tokens against.
func mcpAuthSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-mcp-auth"
}

func mcpAuthSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// renderMCPAuthSecret writes the desktop public key under the filename the
// chart mounts and the file:// issuer both reference, so all three agree. The
// key is public but rides in a Secret to reuse the Cloudflare token's apply path.
func renderMCPAuthSecret(name, namespace, publicKeyPEM string) string {
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
`, name, namespace, desktopMCPPublicKeyFile, publicKeyPEM)
}

// applyMCPAuthSecret delivers the desktop public key into the env's runtime
// pod. A no-op unless the deploy injected a trusted key (a desktop deploy); the
// manifest is piped via stdin so it never appears as an argv element.
func applyMCPAuthSecret(ctx Context, deployInput HelmDeploySpec) error {
	if !deployInput.MCPAuthEnabled || strings.TrimSpace(deployInput.MCPAuthSecretName) == "" {
		return nil
	}
	args := mcpAuthSecretApplyArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply mcp auth public key secret " + deployInput.MCPAuthSecretName)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if strings.TrimSpace(deployInput.MCPAuthPublicKeyPEM) == "" {
		return fmt.Errorf("deploy %s: mcp auth enabled but no public key provided", deployInput.ReleaseName)
	}
	manifest := renderMCPAuthSecret(
		deployInput.MCPAuthSecretName,
		deployInput.Namespace,
		deployInput.MCPAuthPublicKeyPEM,
	)
	cmd := Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply mcp auth secret: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyMCPAuthDeployMetadata records the MCP-auth fields but applies no Secret
// — that happens later in applyMCPAuthSecret. An empty key is a no-op, leaving
// a non-desktop deploy unauthenticated (back-compat). Issuer and audience come
// from the shared conventions so signer, chart, and verifier all agree. The key
// path rides along so the deploy can record it on the env and rethread it next
// time.
func applyMCPAuthDeployMetadata(deployInput *HelmDeploySpec, publicKeyPEM, publicKeyPath string) {
	if deployInput == nil || strings.TrimSpace(publicKeyPEM) == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthPublicKeyPEM = publicKeyPEM
	deployInput.MCPAuthPublicKeyPath = strings.TrimSpace(publicKeyPath)
	deployInput.MCPAuthIssuer = DesktopMCPIssuer()
	deployInput.MCPAuthAudience = MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
	deployInput.MCPAuthSecretName = mcpAuthSecretName(deployInput.ReleaseName)
}
