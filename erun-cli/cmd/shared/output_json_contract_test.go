package shared

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/build"
)

// TestEmptyListingCommandsEmitAnArrayNotNull pins every command whose whole
// structured result is a slice it builds by appending. These reached
// --output json as the literal document `null` when the list was empty, while
// `exec job status` answered `[]` for the same condition and `review list`
// answered a JSON array whenever anything matched -- so one command's result
// changed type with cardinality, and a caller had no way to tell which shape
// it would get.
//
// The nil slices below are what those handlers hand to WriteResult unchanged,
// which is exactly the value the reproduction measured.
func TestEmptyListingCommandsEmitAnArrayNotNull(t *testing.T) {
	cases := []struct {
		command string
		value   any
	}{
		{"jobs list", []platformclient.PlatformJob(nil)},
		{"review list", []platformclient.PlatformReview(nil)},
		{"review queue list", []platformclient.PlatformReview(nil)},
		{"review reviewers", []platformclient.PlatformReviewer(nil)},
		{"platform tenant list", []platformclient.PlatformTenant(nil)},
		{"platform user list", []platformclient.PlatformUser(nil)},
		{"platform environment list", []platformclient.PlatformEnvironment(nil)},
		{"platform context list", []platformclient.PlatformContext(nil)},
		{"build profile", []build.TimingRecordSummary(nil)},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := core.Context{Output: core.OutputJSON, Stdout: &buf, Stderr: &buf}
			if err := ctx.WriteResult(tc.value); err != nil {
				t.Fatalf("write result: %v", err)
			}

			out := bytes.TrimSpace(buf.Bytes())
			if bytes.Equal(out, []byte("null")) {
				t.Fatalf("%s answered an empty listing with the bare document null; a caller "+
					"cannot tell it from a result that was never determined", tc.command)
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("%s did not emit a JSON array for an empty listing: %v (%s)", tc.command, err, out)
			}
			if rows == nil {
				t.Fatalf("%s emitted a document that is not []: %s", tc.command, out)
			}
		})
	}
}

// TestReviewListKeepsOneTypeAcrossCardinalities is the axis the report called
// the sharper form of the defect: not two tools disagreeing, but one command
// disagreeing with itself. A populated list always serialised as an array, so
// a caller that developed against it and iterated the result worked until the
// list came back empty. Both are arrays now.
func TestReviewListKeepsOneTypeAcrossCardinalities(t *testing.T) {
	render := func(t *testing.T, reviews []platformclient.PlatformReview) []json.RawMessage {
		t.Helper()
		var buf bytes.Buffer
		ctx := core.Context{Output: core.OutputJSON, Stdout: &buf, Stderr: &buf}
		if err := ctx.WriteResult(reviews); err != nil {
			t.Fatalf("write result: %v", err)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rows); err != nil {
			t.Fatalf("review list is not a JSON array: %v (%s)", err, buf.String())
		}
		if rows == nil {
			t.Fatalf("review list serialised as null rather than []: %s", buf.String())
		}
		return rows
	}

	if got := len(render(t, nil)); got != 0 {
		t.Fatalf("expected an empty array, got %d rows", got)
	}
	if got := len(render(t, []platformclient.PlatformReview{{ReviewID: "r1"}})); got != 1 {
		t.Fatalf("expected one row, got %d", got)
	}
}
