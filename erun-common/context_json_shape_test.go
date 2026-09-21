package eruncommon

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestWriteResultEmitsAnEmptySliceAsAnArray pins the serialiser half of the
// reported defect. A command returning a Go nil slice -- which is every
// handler that builds its rows by appending -- reached --output json as the
// literal document `null`, so a caller reading .length or iterating the
// result worked while the list had rows and failed on the day it did not.
// null conflates "we queried and nothing matched" with "this was not
// determined"; [] can only mean the first.
//
// The nil slice here is what the same call sites hand over unchanged: the
// platform list commands and the local listing commands all build by append
// and pass the result straight through.
func TestWriteResultEmitsAnEmptySliceAsAnArray(t *testing.T) {
	type row struct {
		ID string `json:"id"`
	}

	cases := []struct {
		name  string
		value any
		rows  int
	}{
		{"nil slice", []row(nil), 0},
		{"empty slice", []row{}, 0},
		{"populated slice", []row{{ID: "r1"}, {ID: "r2"}}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := Context{Output: OutputJSON, Stdout: &buf, Stderr: &buf}
			if err := ctx.WriteResult(tc.value); err != nil {
				t.Fatalf("write result: %v", err)
			}

			out := bytes.TrimSpace(buf.Bytes())
			if bytes.Equal(out, []byte("null")) {
				t.Fatalf("an empty listing serialised as the bare document null: a caller "+
					"cannot tell it apart from a result that was never determined (%s)", out)
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("structured result is not a JSON array: %v (%s)", err, out)
			}
			if rows == nil {
				t.Fatalf("structured result decoded to a nil array, so the document was not []: %s", out)
			}
			if len(rows) != tc.rows {
				t.Fatalf("expected %d rows, got %d (%s)", tc.rows, len(rows), out)
			}
		})
	}
}

// TestWriteResultLeavesStructFieldsAlone is the half that keeps the
// normalisation from destroying information. Inside a struct, absence is part
// of the declared shape: omitempty marks a value that is genuinely not part of
// this result, and a nil slice without it is reported as null on purpose.
// Filling either in would turn a reported absence into a claim -- an empty
// collection asserts that the producer looked and found nothing, which is a
// different fact from never having looked.
func TestWriteResultLeavesStructFieldsAlone(t *testing.T) {
	type optional struct {
		Omitted []string `json:"omitted,omitempty"`
		Present []string `json:"present"`
	}

	var buf bytes.Buffer
	ctx := Context{Output: OutputJSON, Stdout: &buf, Stderr: &buf}
	if err := ctx.WriteResult(optional{}); err != nil {
		t.Fatalf("write result: %v", err)
	}

	encoded := bytes.TrimSpace(buf.Bytes())
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("structured result is not a JSON object: %v", err)
	}
	if _, present := document["omitted"]; present {
		t.Fatalf("an omitempty field was filled in, asserting a value the producer never reported: %s", encoded)
	}
	if string(document["present"]) != "null" {
		t.Fatalf("expected the non-omitempty nil field to stay null, got %s (%s)", document["present"], encoded)
	}
}

// TestWriteResultKeepsTextModeSilent guards the normalisation from leaking into
// the default human-facing mode, where a command streams its own prose and the
// structured writer must stay out of the way.
func TestWriteResultKeepsTextModeSilent(t *testing.T) {
	var buf bytes.Buffer
	ctx := Context{Output: OutputText, Stdout: &buf, Stderr: &buf}
	if err := ctx.WriteResult([]string(nil)); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("text mode wrote a structured result: %q", buf.String())
	}
}
