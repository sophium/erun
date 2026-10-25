package jobhost

import (
	"os"
)

// CurrentJobHostname is the pod hostname a job supervisor stamps and reconciles against.
// Empty on error, so reconciliation falls back to its guess instead of failing.
func CurrentJobHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
