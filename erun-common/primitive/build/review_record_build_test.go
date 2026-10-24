package build

import (
	"testing"
)

func TestBuildInconclusiveSignaturesReturnsACopy(t *testing.T) {
	signatures := BuildInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("expected the classifier to carry signatures")
	}
	signatures[0] = "mutated"
	if BuildInconclusiveSignatures()[0] == "mutated" {
		t.Fatal("mutating the returned slice changed the classifier's own list")
	}
}
