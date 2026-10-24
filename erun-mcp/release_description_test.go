package erunmcp

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// `erun release` only marks source control; its description must not claim to
// publish artifacts.
func TestReleaseToolDoesNotClaimItPublishesArtifacts(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin))
	tools := listTools(t, session)
	if len(tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	var release string
	found := false
	for _, tool := range tools {
		if tool.Name == "release" {
			release, found = tool.Description, true
			break
		}
	}
	if !found {
		t.Fatal("the release tool is not registered")
	}

	for _, claim := range []string{
		"builds and publishes",
		"reads each one back from the registry",
		"fails while nothing is public",
	} {
		if strings.Contains(release, claim) {
			t.Errorf("the release tool description claims %q, but `erun release` never builds, publishes, or verifies an artifact; it would have a caller treat a completed release as a deployable version.\ndescription: %s", claim, release)
		}
	}

	// It must name what does publish, or the description is a dead end.
	for _, next := range []string{"build --release", "push --version"} {
		if !strings.Contains(release, next) {
			t.Errorf("the release tool description does not name %q, so a caller whose release published nothing has no next step from the description alone.\ndescription: %s", next, release)
		}
	}

	if !strings.Contains(release, "source control") {
		t.Errorf("the release tool description does not say the release marks source control only.\ndescription: %s", release)
	}
}
