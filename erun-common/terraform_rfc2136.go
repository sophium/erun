package eruncommon

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// terraformDNS01ProviderRFC2136 is the cluster-edge module's dns01_provider
// value that switches cert-manager's DNS-01 solver to RFC2136 (DNS UPDATE +
// TSIG) against the platform's own PowerDNS, instead of the Cloudflare API.
const terraformDNS01ProviderRFC2136 = "powerdns-rfc2136"

// terraformRFC2136DefaultIssuerName, terraformRFC2136SecretSuffix, and
// terraformRFC2136SecretKey mirror terraform-erun-cluster-edge's own locals
// (variables.tf's issuer_name default, and
// `tsig_secret_name = "${var.issuer_name}-rfc2136-tsig"` / key "tsig-secret"
// in main.tf) — the module's own materialized Secret that the rendered Issuer
// references, not the erun-powerdns chart's source secret. Reading it back is
// what lets a repeat plan/apply resupply the same value the module last wrote.
const (
	terraformRFC2136DefaultIssuerName      = "erun-cloudflare"
	terraformRFC2136SecretSuffix           = "-rfc2136-tsig"
	terraformRFC2136SecretKey              = "tsig-secret"
	terraformRFC2136DefaultIssuerNamespace = "cert-manager"
)

// terraformRFC2136Requirement is what a resolved <env>.tfvars says about the
// cluster-edge module's RFC2136 TSIG secret: whether this env's dns01_provider
// needs it, and where erun should read it from. KubernetesContext is not part
// of the tfvars — it is the env's own resolved kubectl context, filled in by
// the caller so the read targets the same cluster every other kubectl call
// against this env uses, instead of silently falling back to whatever
// kubectl's ambient current-context happens to be on the operator's machine.
type terraformRFC2136Requirement struct {
	Needed            bool
	Namespace         string
	SecretName        string
	SecretKey         string
	KubernetesContext string
}

// resolveTerraformRFC2136Requirement reads the resolved var file — the same
// file erun already locates for -var-file — to learn whether this env's
// dns01_provider is "powerdns-rfc2136". PlatformConfig carries no such field:
// dns01_provider is purely a terraform-module input, so the tfvars file is the
// only place erun can learn it. It is parsed with a small literal-string
// scanner (parseTerraformTFVarsStrings) that walks tokens and skips comments
// properly, rather than a regex matched against the raw text, so a value
// sitting in a comment or a differently-quoted line can't produce a false
// negative on what is otherwise a security-relevant decision. A var file that
// can't be parsed as simple attributes is a real error; one that parses but
// carries no dns01_provider (or a non-literal expression for it) resolves to
// the module's own default ("cloudflare" — not needed), matching current
// behavior for every env that isn't using powerdns-rfc2136.
func resolveTerraformRFC2136Requirement(dir, varFile string) (terraformRFC2136Requirement, error) {
	if strings.TrimSpace(varFile) == "" {
		return terraformRFC2136Requirement{}, nil
	}
	attrs, err := parseTerraformTFVarsStrings(filepath.Join(dir, varFile))
	if err != nil {
		return terraformRFC2136Requirement{}, fmt.Errorf("terraform: reading %s: %w", varFile, err)
	}
	if attrs["dns01_provider"] != terraformDNS01ProviderRFC2136 {
		return terraformRFC2136Requirement{}, nil
	}

	issuerName := strings.TrimSpace(attrs["issuer_name"])
	if issuerName == "" {
		issuerName = terraformRFC2136DefaultIssuerName
	}

	// Mirrors the module's own locals: env_namespace falls back to env_label,
	// and the issuer namespace falls back to the cert-manager namespace var
	// (default "cert-manager") when neither is set.
	namespace := strings.TrimSpace(attrs["env_namespace"])
	if namespace == "" {
		namespace = strings.TrimSpace(attrs["env_label"])
	}
	if namespace == "" {
		namespace = strings.TrimSpace(attrs["namespace"])
	}
	if namespace == "" {
		namespace = terraformRFC2136DefaultIssuerNamespace
	}

	return terraformRFC2136Requirement{
		Needed:     true,
		Namespace:  namespace,
		SecretName: issuerName + terraformRFC2136SecretSuffix,
		SecretKey:  terraformRFC2136SecretKey,
	}, nil
}

// readTerraformRFC2136Secret reads the RFC2136 TSIG key material back from
// its Kubernetes Secret so the operator never has to export it by hand. The
// read is pinned to the env's own resolved kubernetes context (the same one
// every other kubectl call against this env uses) rather than left to
// kubectl's ambient current-context, and it is traced so the exact command —
// including which context it targeted — is visible even in --dry-run, since
// this read always runs up front regardless of DryRun. On failure it
// distinguishes the Secret genuinely being absent (create it) from the read
// itself failing for some other reason such as an RBAC denial or the wrong
// context (fix credentials/context) — two different remedies that a bare
// "could not be read: exit status 1" collapsed into one.
func readTerraformRFC2136Secret(ctx Context, requirement terraformRFC2136Requirement) (string, error) {
	jsonPath := "jsonpath={.data." + strings.ReplaceAll(requirement.SecretKey, ".", `\.`) + "}"
	args := make([]string, 0, 8)
	if strings.TrimSpace(requirement.KubernetesContext) != "" {
		args = append(args, "--context", requirement.KubernetesContext)
	}
	args = append(args, "-n", requirement.Namespace, "get", "secret", requirement.SecretName, "-o", jsonPath)
	ctx.TraceCommand("", "kubectl", args...)

	output, stderr, err := runObserveKubectl(args)
	if err != nil {
		return "", terraformRFC2136ReadError(requirement, stderr, err)
	}
	encoded := strings.TrimSpace(string(output))
	contextLabel := terraformKubernetesContextLabel(requirement.KubernetesContext)
	if encoded == "" {
		return "", fmt.Errorf("terraform: dns01_provider %q requires the RFC2136 TSIG secret, and Kubernetes Secret %s/%s was read via %s, but has no value at key %q",
			terraformDNS01ProviderRFC2136, requirement.Namespace, requirement.SecretName, contextLabel, requirement.SecretKey)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || strings.TrimSpace(string(decoded)) == "" {
		return "", fmt.Errorf("terraform: dns01_provider %q requires the RFC2136 TSIG secret, and Kubernetes Secret %s/%s was read via %s, but holds no readable value at key %q",
			terraformDNS01ProviderRFC2136, requirement.Namespace, requirement.SecretName, contextLabel, requirement.SecretKey)
	}
	return string(decoded), nil
}

// terraformRFC2136ReadError distinguishes the Secret being absent (kubectl's
// own NotFound) from every other read failure (RBAC denial, wrong/unknown
// context, unreachable cluster, ...), since those two situations have
// different remedies. The non-absent branch carries kubectl's own stderr
// verbatim — a Forbidden response already names the exact denied principal
// (e.g. `User "system:serviceaccount:...": cannot get resource "secrets"`),
// which is more precise than erun guessing at who attempted the read.
func terraformRFC2136ReadError(requirement terraformRFC2136Requirement, stderr string, err error) error {
	contextLabel := terraformKubernetesContextLabel(requirement.KubernetesContext)
	if isKubectlNotFound(stderr) {
		return fmt.Errorf("terraform: dns01_provider %q requires the RFC2136 TSIG secret, but Kubernetes Secret %s/%s does not exist, read via %s — create it (e.g. via the erun-powerdns chart) or point issuer_name/env_namespace in the resolved .tfvars at where it actually lives",
			terraformDNS01ProviderRFC2136, requirement.Namespace, requirement.SecretName, contextLabel)
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("terraform: dns01_provider %q requires the RFC2136 TSIG secret, but Kubernetes Secret %s/%s could not be read (key %q) via %s — this is not a not-found, so treat it as a credentials/context/RBAC problem rather than a missing secret: %s",
		terraformDNS01ProviderRFC2136, requirement.Namespace, requirement.SecretName, requirement.SecretKey, contextLabel, detail)
}

// terraformKubernetesContextLabel renders the context a kubectl read targeted
// for an error/trace message. An empty context means erun had none resolved
// for this env and let kubectl fall back to its own ambient current-context —
// naming that explicitly, rather than silently omitting it, is what makes
// that fallback visible to an operator debugging a wrong-cluster read.
func terraformKubernetesContextLabel(context string) string {
	if strings.TrimSpace(context) == "" {
		return "kubectl's ambient current-context (no kubernetes context is configured for this environment)"
	}
	return fmt.Sprintf("kubernetes context %q", context)
}

// parseTerraformTFVarsStrings scans a resolved .tfvars file for top-level
// `identifier = "value"` assignments — the whole grammar the cluster-edge
// module's string variables need. It correctly skips "#"/"//" line comments
// and "/* */" block comments, and honors backslash escapes inside quoted
// strings, so it can't be fooled by a value that only appears in a comment or
// on a differently-formatted line. An attribute whose value isn't a plain
// quoted string (a list, a heredoc, an expression) is simply absent from the
// result; callers treat that as "could not determine" rather than guess.
func parseTerraformTFVarsStrings(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return scanTerraformTFVarsStrings(string(data)), nil
}

func scanTerraformTFVarsStrings(src string) map[string]string {
	result := make(map[string]string)
	n := len(src)
	i := 0
	for i < n {
		i = skipTerraformTFVarsTrivia(src, i)
		if i >= n {
			break
		}
		name, i2 := scanTerraformTFVarsIdentifier(src, i)
		if name == "" {
			// Not the start of an identifier (e.g. a stray `[`/`{`); skip one
			// byte so the scanner always makes progress.
			i++
			continue
		}
		i = skipTerraformTFVarsTrivia(src, i2)
		if i >= n || src[i] != '=' {
			i = i2
			continue
		}
		i = skipTerraformTFVarsTrivia(src, i+1) // consume '=' then its trivia
		if value, next, ok := scanTerraformTFVarsStringValue(src, i); ok {
			result[name] = value
			i = next
			continue
		}
		// Not a plain string literal: skip to end of line rather than guess.
		i = skipTerraformTFVarsLine(src, i)
	}
	return result
}

// scanTerraformTFVarsIdentifier reads the identifier starting at src[i].
// name=="" means src[i] isn't an identifier character at all.
func scanTerraformTFVarsIdentifier(src string, i int) (name string, next int) {
	n := len(src)
	start := i
	for i < n && isTerraformTFVarsIdentChar(src[i]) {
		i++
	}
	return src[start:i], i
}

// scanTerraformTFVarsStringValue reads a quoted-string value at src[i], the
// only value shape this scanner resolves.
func scanTerraformTFVarsStringValue(src string, i int) (value string, next int, ok bool) {
	if i >= len(src) || src[i] != '"' {
		return "", i, false
	}
	return readTerraformTFVarsQuotedString(src, i)
}

func skipTerraformTFVarsLine(src string, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

func isTerraformTFVarsIdentChar(b byte) bool {
	return b == '_' || b == '-' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// skipTerraformTFVarsTrivia advances past whitespace and comments —
// everything HCL treats as insignificant between tokens — repeating until a
// pass makes no further progress (a comment can be followed by more
// whitespace, another comment, and so on).
func skipTerraformTFVarsTrivia(src string, i int) int {
	for {
		start := i
		i = skipTerraformTFVarsWhitespace(src, i)
		i = skipTerraformTFVarsLineComment(src, i)
		i = skipTerraformTFVarsBlockComment(src, i)
		if i == start {
			return i
		}
	}
}

func skipTerraformTFVarsWhitespace(src string, i int) int {
	n := len(src)
	for i < n && isTerraformTFVarsSpace(src[i]) {
		i++
	}
	return i
}

func isTerraformTFVarsSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func skipTerraformTFVarsLineComment(src string, i int) int {
	if !strings.HasPrefix(src[i:], "#") && !strings.HasPrefix(src[i:], "//") {
		return i
	}
	return skipTerraformTFVarsLine(src, i)
}

// skipTerraformTFVarsBlockComment skips a "/* ... */" comment. An
// unterminated comment runs to the end of the file, matching HCL.
func skipTerraformTFVarsBlockComment(src string, i int) int {
	if !strings.HasPrefix(src[i:], "/*") {
		return i
	}
	if end := strings.Index(src[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(src)
}

// readTerraformTFVarsQuotedString reads a double-quoted string literal
// starting at src[i] (which must be '"'), honoring backslash escapes.
// ok=false on an unterminated or multi-line string.
func readTerraformTFVarsQuotedString(src string, i int) (value string, next int, ok bool) {
	n := len(src)
	i++ // consume opening quote
	var b strings.Builder
	for i < n {
		switch src[i] {
		case '"':
			return b.String(), i + 1, true
		case '\\':
			if i+1 >= n {
				return "", 0, false
			}
			b.WriteByte(src[i+1])
			i += 2
		case '\n':
			return "", 0, false
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return "", 0, false
}
