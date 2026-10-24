package terraform

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// terraformDNS01ProviderRFC2136 switches the cluster-edge DNS-01 solver to
// RFC2136 against the platform's PowerDNS.
const terraformDNS01ProviderRFC2136 = "powerdns-rfc2136"

// These mirror terraform-erun-cluster-edge's tsig_secret_name locals: the
// module's own materialized Secret, read back so a repeat apply resupplies it.
const (
	terraformRFC2136DefaultIssuerName      = "erun-cloudflare"
	terraformRFC2136SecretSuffix           = "-rfc2136-tsig"
	terraformRFC2136SecretKey              = "tsig-secret"
	terraformRFC2136DefaultIssuerNamespace = "cert-manager"
)

// KubernetesContext comes from the env, not the tfvars, so the read never falls
// back to kubectl's ambient context.
type terraformRFC2136Requirement struct {
	Needed            bool
	Namespace         string
	SecretName        string
	SecretKey         string
	KubernetesContext string
}

// resolveTerraformRFC2136Requirement learns dns01_provider from the var file,
// its only source. A token scanner, not a regex, keeps commented values from
// deciding this security-relevant choice; an absent value means the module
// default (cloudflare).
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

	// Mirrors the module's namespace fallbacks.
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

// readTerraformRFC2136Secret runs even under --dry-run and is traced with its
// context.
func readTerraformRFC2136Secret(ctx core.Context, requirement terraformRFC2136Requirement) (string, error) {
	jsonPath := "jsonpath={.data." + strings.ReplaceAll(requirement.SecretKey, ".", `\.`) + "}"
	args := make([]string, 0, 8)
	if strings.TrimSpace(requirement.KubernetesContext) != "" {
		args = append(args, "--context", requirement.KubernetesContext)
	}
	args = append(args, "-n", requirement.Namespace, "get", "secret", requirement.SecretName, "-o", jsonPath)
	ctx.TraceCommand("", "kubectl", args...)

	output, stderr, err := deploy.RunObserveKubectl(args)
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

// terraformRFC2136ReadError separates NotFound from other failures, which need
// a different remedy; kubectl's stderr names the denied principal.
func terraformRFC2136ReadError(requirement terraformRFC2136Requirement, stderr string, err error) error {
	contextLabel := terraformKubernetesContextLabel(requirement.KubernetesContext)
	if clusterread.IsKubectlNotFound(stderr) {
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

// An empty context means kubectl's ambient one; say so explicitly.
func terraformKubernetesContextLabel(context string) string {
	if strings.TrimSpace(context) == "" {
		return "kubectl's ambient current-context (no kubernetes context is configured for this environment)"
	}
	return fmt.Sprintf("kubernetes context %q", context)
}

// parseTerraformTFVarsStrings returns top-level `name = "string"` attributes,
// skipping comments; non-string values are absent rather than guessed.
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
			// Skip one byte so the scanner always makes progress.
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
		i = skipTerraformTFVarsLine(src, i)
	}
	return result
}

// name=="" means src[i] isn't an identifier character.
func scanTerraformTFVarsIdentifier(src string, i int) (name string, next int) {
	n := len(src)
	start := i
	for i < n && isTerraformTFVarsIdentChar(src[i]) {
		i++
	}
	return src[start:i], i
}

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

// An unterminated block comment runs to EOF, matching HCL.
func skipTerraformTFVarsBlockComment(src string, i int) int {
	if !strings.HasPrefix(src[i:], "/*") {
		return i
	}
	if end := strings.Index(src[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(src)
}

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
