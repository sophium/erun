package core

import (
	"bytes"
	"encoding/json"
	"testing"
)

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

// TestWriteResultLeavesStructFieldsAlone: nested absence is meaningful and must stay null.
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
