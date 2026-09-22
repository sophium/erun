package erunmcp

import (
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestReleaseToolDoesNotClaimItPublishesArtifacts reproduces the contradiction
// that left a released version with nothing to deploy.
//
// `erun release` marks source control and nothing else, so a version's images
// and charts are published by a separate command. The release tool's
// description promised the opposite -- that the release builds and publishes
// that version's artifacts and reads each one back before it pushes the tag --
// so a caller reads a completed release as a deployable version, reaches for
// `erun deploy --version <v>`, and only meets the failure there, after the tag
// is already public and the release has been announced as cut.
//
// A description is not decorative: it is the whole contract an MCP caller has,
// and this one named a capability the command does not have.
func TestReleaseToolDoesNotClaimItPublishesArtifacts(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
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

	// It must not advertise artifact production it does not perform. These are
	// the claims the stale description made, as a caller would read them.
	for _, claim := range []string{
		"builds and publishes",
		"reads each one back from the registry",
		"fails while nothing is public",
	} {
		if strings.Contains(release, claim) {
			t.Errorf("the release tool description claims %q, but `erun release` never builds, publishes, or verifies an artifact; it would have a caller treat a completed release as a deployable version.\ndescription: %s", claim, release)
		}
	}

	// A caller told the release publishes nothing needs to be told what does,
	// or the description is accurate and still a dead end.
	for _, next := range []string{"build --release", "push --version"} {
		if !strings.Contains(release, next) {
			t.Errorf("the release tool description does not name %q, so a caller whose release published nothing has no next step from the description alone.\ndescription: %s", next, release)
		}
	}

	if !strings.Contains(release, "source control") {
		t.Errorf("the release tool description does not say the release marks source control only.\ndescription: %s", release)
	}
}
