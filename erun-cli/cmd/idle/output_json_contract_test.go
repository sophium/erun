package idle

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// Text mode is the default and must stay prose: the assertion above passes
// trivially for a command that always emits JSON, which would trade one silent
// substitution for another.
func TestTextOutputStaysProse(t *testing.T) {
	var buf bytes.Buffer
	ctx := core.Context{Output: core.OutputText, Stdout: &buf, Stderr: &buf}
	if err := WriteIdleResult(ctx, activity.EnvironmentIdleStatus{SecondsUntilStop: 300}, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatalf("default text mode emitted JSON: %q", buf.String())
	}
}
