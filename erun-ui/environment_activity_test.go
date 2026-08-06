package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The sidebar's busy line comes from the environment's own idle markers, so the
// reduction from markers to "what is keeping it busy" is what has to be right:
// a held lease is the only signal that names the work, and everything else must
// read in the operator's language rather than by its wire name.

func TestEnvironmentBusyFromIdleStatus(t *testing.T) {
	activeMarker := func(name string) eruncommon.EnvironmentIdleMarker {
		return eruncommon.EnvironmentIdleMarker{Name: name, Idle: false, Reason: "recent activity"}
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		status     eruncommon.EnvironmentIdleStatus
		wantBusy   bool
		wantDetail string
	}{
		{
			name: "a quiet environment is not busy",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				{Name: "working-hours", Idle: false},
				{Name: eruncommon.ActivityKindMCP, Idle: true},
			}},
		},
		{
			// working-hours is never activity — it is inverted (not idle means
			// "inside working hours"), so counting it would make every
			// environment busy all day.
			name: "inside working hours alone is not activity",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker("working-hours"),
			}},
		},
		{
			name: "a held lease names the work",
			status: eruncommon.EnvironmentIdleStatus{
				Leases: []eruncommon.EnvironmentActivityLease{
					{ID: "gradle-build", Name: "gradle-build", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []eruncommon.EnvironmentIdleMarker{activeMarker("lease")},
			},
			wantBusy:   true,
			wantDetail: "holding: gradle-build",
		},
		{
			// The lease is preferred over a bare marker precisely because it is
			// the one signal that can say what the work is.
			name: "a lease wins over a generic marker",
			status: eruncommon.EnvironmentIdleStatus{
				Leases: []eruncommon.EnvironmentActivityLease{
					{ID: "agent-run", Name: "agent-run", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []eruncommon.EnvironmentIdleMarker{activeMarker(eruncommon.ActivityKindMCP)},
			},
			wantBusy:   true,
			wantDetail: "holding: agent-run",
		},
		{
			name: "sampled processes describe themselves",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker(eruncommon.ActivityKindProcess),
			}},
			wantBusy:   true,
			wantDetail: "running build or agent processes",
		},
		{
			name: "an agent over MCP is named",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker(eruncommon.ActivityKindMCP),
			}},
			wantBusy:   true,
			wantDetail: "an agent is driving it over MCP",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			busy, detail := environmentBusyFromIdleStatus(testCase.status)
			if busy != testCase.wantBusy || detail != testCase.wantDetail {
				t.Errorf("got busy=%v detail=%q, want busy=%v detail=%q", busy, detail, testCase.wantBusy, testCase.wantDetail)
			}
		})
	}
}
