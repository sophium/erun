package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	snapshotFlagName   = "snapshot"
	noSnapshotFlagName = "no-snapshot"
)

func addSnapshotFlags(cmd *cobra.Command, snapshot, noSnapshot *bool, usage string) {
	if cmd == nil {
		return
	}
	cmd.Flags().BoolVar(snapshot, snapshotFlagName, true, usage)
	cmd.Flags().BoolVar(noSnapshot, noSnapshotFlagName, false, "Alias for --snapshot=false")
}

func resolveSnapshotFlagOverride(cmd *cobra.Command, snapshot, noSnapshot bool) (*bool, error) {
	if cmd == nil {
		return nil, nil
	}

	snapshotFlag := cmd.Flags().Lookup(snapshotFlagName)
	noSnapshotFlag := cmd.Flags().Lookup(noSnapshotFlagName)
	snapshotChanged := snapshotFlag != nil && snapshotFlag.Changed
	noSnapshotChanged := noSnapshotFlag != nil && noSnapshotFlag.Changed
	if !snapshotChanged && !noSnapshotChanged {
		return nil, nil
	}

	value := snapshot
	if noSnapshotChanged {
		inverted := !noSnapshot
		if snapshotChanged && value != inverted {
			return nil, fmt.Errorf("cannot use --%s and --%s with conflicting values", snapshotFlagName, noSnapshotFlagName)
		}
		value = inverted
	}
	return &value, nil
}
