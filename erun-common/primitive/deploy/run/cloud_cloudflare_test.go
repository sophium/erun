package run

import (
	"strings"
	"testing"
)

func TestRenderCloudflareCredentialsSecretRedactsAndQuotes(t *testing.T) {
	manifest := renderCloudflareCredentialsSecret("acme-dev-cloudflare", "acme-dev", `tok"with\quote`)
	if !strings.Contains(manifest, "CLOUDFLARE_API_TOKEN: \"tok\\\"with\\\\quote\"") {
		t.Fatalf("token not safely quoted in manifest:\n%s", manifest)
	}
	// Only the sensitive token belongs in the Secret; the account id rides as a
	// non-secret helm value.
	if strings.Contains(manifest, "CLOUDFLARE_ACCOUNT_ID") {
		t.Fatalf("account id must not be in the secret manifest:\n%s", manifest)
	}
}
