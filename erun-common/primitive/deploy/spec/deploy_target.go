package spec

import (
	"strings"
)

// deployResetsDatabase arms the postgres reset for snapshots only; it takes no
// bool so callers cannot force it unconditional.
func deployResetsDatabase(version string) bool {
	return strings.Contains(strings.TrimSpace(version), "-snapshot-")
}
