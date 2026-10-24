package eruncommon

import (
	"testing"

	"github.com/sophium/erun/erun-common/primitive/build"
)

func TestBuildInconclusiveSignaturesReturnsACopy(t *testing.T) {
	signatures := build.BuildInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("expected the classifier to carry signatures")
	}
	signatures[0] = "mutated"
	if build.BuildInconclusiveSignatures()[0] == "mutated" {
		t.Fatal("mutating the returned slice changed the classifier's own list")
	}
}
