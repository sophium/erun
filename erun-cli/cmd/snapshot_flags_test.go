package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveSnapshotFlagOverride(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    *bool
		wantErr string
	}{
		{name: "unchanged", args: nil},
		{name: "snapshot false", args: []string{"--snapshot=false"}, want: boolPtr(false)},
		{name: "snapshot true", args: []string{"--snapshot"}, want: boolPtr(true)},
		{name: "no snapshot", args: []string{"--no-snapshot"}, want: boolPtr(false)},
		{name: "no snapshot false", args: []string{"--no-snapshot=false"}, want: boolPtr(true)},
		{name: "same false via both flags", args: []string{"--snapshot=false", "--no-snapshot"}, want: boolPtr(false)},
		{name: "conflict", args: []string{"--snapshot", "--no-snapshot"}, wantErr: "cannot use --snapshot and --no-snapshot with conflicting values"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var snapshot bool
			var noSnapshot bool
			cmd := &cobra.Command{Use: "test"}
			addSnapshotFlags(cmd, &snapshot, &noSnapshot, "test usage")
			if err := cmd.Flags().Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			got, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("expected error %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("resolveSnapshotFlagOverride failed: %v", err)
			}
			if !equalBoolPointer(got, tt.want) {
				t.Fatalf("unexpected override: got=%v want=%v", got, tt.want)
			}
		})
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func equalBoolPointer(left, right *bool) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}
