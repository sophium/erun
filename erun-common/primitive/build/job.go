package build

import (
	"os"
)

// CurrentJobHostname is the pod hostname a job supervisor stamps at start and
// compares against at reconcile. Empty on error rather than failing the
// caller: a job record's Hostname is then left unset, and reconciliation
// falls back to the older "most likely replaced" guess instead of
// hard-failing on it.
func CurrentJobHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
