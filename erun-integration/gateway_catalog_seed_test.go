package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// gatewayCatalogSeed is the erun-level gateway catalog (root config's
// `openrouter:` block) a scenario needs.
type gatewayCatalogSeed struct {
	baseURL      string
	authTokenRef string
	defaultModel string
	models       []gatewayModelSeed
}

type gatewayModelSeed struct {
	id      string
	context int
}

// defaultGatewayCatalogSeed lists one model the gateway can drive and one it
// cannot (deepseek-v4.1-flash requires its own reasoning echoed back), so a
// scenario can reach both sides of that refusal against the same catalog.
func defaultGatewayCatalogSeed() gatewayCatalogSeed {
	return gatewayCatalogSeed{
		baseURL:      "https://openrouter.ai/api",
		authTokenRef: "erun-claude-gateway",
		defaultModel: "deepseek/deepseek-v4-pro-0813",
		models: []gatewayModelSeed{
			{id: "deepseek/deepseek-v4.1-flash", context: 1048576},
			{id: "deepseek/deepseek-v4-pro-0813", context: 1024000},
		},
	}
}

// seedGatewayCatalog appends the catalog to the isolated root config. It
// appends rather than overwrites, so call it after any seeder that rewrites
// config.yaml whole (seedCloudContextConfig, seedERunCloudProviderAlias), or
// that seeder drops the catalog.
func seedGatewayCatalog(t testing.TB, setup env.Setup, catalog gatewayCatalogSeed) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	path := filepath.Join(root, "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read root config %s: %v", path, err)
	}
	var sb strings.Builder
	sb.WriteString("openrouter:\n")
	sb.WriteString("  baseurl: " + catalog.baseURL + "\n")
	if catalog.authTokenRef != "" {
		sb.WriteString("  authtokenref: " + catalog.authTokenRef + "\n")
	}
	if catalog.defaultModel != "" {
		sb.WriteString("  defaultmodel: " + catalog.defaultModel + "\n")
	}
	if len(catalog.models) > 0 {
		sb.WriteString("  models:\n")
		for _, model := range catalog.models {
			sb.WriteString("    - id: " + model.id + "\n")
			if model.context > 0 {
				sb.WriteString("      context: " + strconv.Itoa(model.context) + "\n")
			}
		}
	}
	if err := os.WriteFile(path, append(existing, []byte(sb.String())...), 0o644); err != nil {
		t.Fatalf("write root config %s: %v", path, err)
	}
}
