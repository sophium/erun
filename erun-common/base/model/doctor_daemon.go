package model

import (
	"strings"
)

// DoctorDockerDaemonKind is resolved from the environment: pruning any daemon
// other than the build's reclaims nothing yet still reports success.
type DoctorDockerDaemonKind string

const (
	// DoctorDockerDaemonDindSidecar is the runtime pod's erun-dind sidecar, where builds run.
	DoctorDockerDaemonDindSidecar DoctorDockerDaemonKind = "dind-sidecar"

	// DoctorDockerDaemonHost is this machine's daemon, used by host environments.
	DoctorDockerDaemonHost DoctorDockerDaemonKind = "host"

	// DoctorDockerDaemonNone means no daemon holds the build images; Where says why.
	DoctorDockerDaemonNone DoctorDockerDaemonKind = "none"
)

// DoctorDockerDaemon is the daemon a docker-storage read or prune acts on.
type DoctorDockerDaemon struct {
	Kind DoctorDockerDaemonKind
	// Container is empty for a host daemon.
	Container string
	Label     string
	// Where explains which daemon this is, or why there is none; it stands alone
	// as the reason a prune cannot run.
	Where string
}

// DoctorDockerDaemonUnavailableError reports that the environment has no build
// daemon; it is not a transient reachability failure.
type DoctorDockerDaemonUnavailableError struct {
	Daemon DoctorDockerDaemon
}

func (e DoctorDockerDaemonUnavailableError) Error() string {
	if strings.TrimSpace(e.Daemon.Where) == "" {
		return "no docker daemon in this environment holds its build images"
	}
	return e.Daemon.Where
}

// Unavailable reports whether no daemon holds this environment's build images.
func (d DoctorDockerDaemon) Unavailable() bool {
	return d.Kind == DoctorDockerDaemonNone
}

// Report describes the daemon and where it is, or why there is none.
func (d DoctorDockerDaemon) Report() string {
	return d.Label + " — " + d.Where
}
