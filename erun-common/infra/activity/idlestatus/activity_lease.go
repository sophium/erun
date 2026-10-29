package idlestatus

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

const (

	// environmentActivityLeaseMarker lets leases block idle-stop through the shared marker predicate.
	environmentActivityLeaseMarker = "lease"
)

func leaseIdleMarker(leases []model.EnvironmentActivityLease, now time.Time) model.EnvironmentIdleMarker {
	marker := model.EnvironmentIdleMarker{Name: environmentActivityLeaseMarker}
	if len(leases) == 0 {
		marker.Idle = true
		marker.Reason = "no work leases held"
		return marker
	}
	names := make([]string, 0, len(leases))
	var lastRenewed time.Time
	var expiry time.Time
	for _, lease := range leases {
		names = append(names, lease.Name)
		if lease.RenewedAt.After(lastRenewed) {
			lastRenewed = lease.RenewedAt
		}
		if lease.StartedAt.After(lastRenewed) {
			lastRenewed = lease.StartedAt
		}
		if lease.ExpiresAt.After(expiry) {
			expiry = lease.ExpiresAt
		}
	}
	marker.Idle = false
	marker.Reason = "held by " + strings.Join(names, ", ")
	marker.LastActivity = lastRenewed
	marker.LastSeen = lastRenewed
	marker.SecondsRemaining = secondsRemaining(expiry.Sub(now))
	return marker
}
