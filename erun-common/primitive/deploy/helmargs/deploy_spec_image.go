package helmargs

import (
	"strings"
)

// HelmOIDCAllowedIssuersSetArgs omits an empty value: --set always beats -f and
// would clobber values.<env>.yaml.
func HelmOIDCAllowedIssuersSetArgs(issuers string) []string {
	if strings.TrimSpace(issuers) == "" {
		return nil
	}
	return []string{"--set-string", "api.oidcAllowedIssuers=" + EscapeHelmSetValue(issuers)}
}
