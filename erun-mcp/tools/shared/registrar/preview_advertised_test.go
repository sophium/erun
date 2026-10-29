package registrar

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func previewAdvertisedInDescription(tool *mcp.Tool) bool {
	return strings.HasSuffix(strings.TrimSpace(tool.Description), PreviewAdvertised)
}

// embeddedPreview reaches `preview` through an embedded struct.
type embeddedPreview struct {
	Preview bool `json:"preview,omitempty"`
}

type promotedPreview struct {
	embeddedPreview
	Other bool `json:"other,omitempty"`
}

type noPreview struct {
	Other bool `json:"other,omitempty"`
}

func TestAdvertisePreviewFollowsTheInputType(t *testing.T) {
	declared := &mcp.Tool{Name: "declared", Description: "Does a thing."}
	AdvertisePreview[struct {
		Preview bool `json:"preview,omitempty"`
	}](declared)

	promoted := &mcp.Tool{Name: "promoted", Description: "Does a thing."}
	AdvertisePreview[promotedPreview](promoted)

	absent := &mcp.Tool{Name: "absent", Description: "Does a thing."}
	AdvertisePreview[noPreview](absent)

	for _, tool := range []*mcp.Tool{declared, promoted} {
		if !previewAdvertisedInDescription(tool) {
			t.Errorf("%s: accepts preview but the description does not say so: %q", tool.Name, tool.Description)
		}
		if n := strings.Count(tool.Description, PreviewAdvertised); n != 1 {
			t.Errorf("%s: sentence appears %d times, want 1: %q", tool.Name, n, tool.Description)
		}
	}

	if previewAdvertisedInDescription(absent) {
		t.Errorf("absent: description advertises preview but the input type has no such property: %q", absent.Description)
	}

	// Re-registering must not append the sentence twice.
	AdvertisePreview[promotedPreview](promoted)
	if n := strings.Count(promoted.Description, PreviewAdvertised); n != 1 {
		t.Errorf("promoted: re-registering produced the sentence %d times: %q", n, promoted.Description)
	}
}

// No registered tool trips this guard, so it is exercised directly.
func TestAdvertisePreviewRefusesAFalseClaim(t *testing.T) {
	tool := &mcp.Tool{Name: "synthetic", Description: "Does a thing. " + PreviewAdvertised}

	defer func() {
		if recover() == nil {
			t.Error("advertisePreview accepted a description claiming a preview its input type cannot deliver, so the claim would ship")
		}
	}()

	AdvertisePreview[noPreview](tool)
}
