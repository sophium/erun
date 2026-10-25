package runtimeview

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestEnvironmentBusyFromIdleStatus(t *testing.T) {
	activeMarker := func(name string) model.EnvironmentIdleMarker {
		return model.EnvironmentIdleMarker{Name: name, Idle: false, Reason: "recent activity"}
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		status     model.EnvironmentIdleStatus
		wantBusy   bool
		wantDetail string
	}{
		{
			name: "a quiet environment is not busy",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				{Name: "working-hours", Idle: false},
				{Name: config.ActivityKindMCP, Idle: true},
			}},
		},
		{
			// working-hours is inverted, so counting it would make every env busy all day.
			name: "inside working hours alone is not activity",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker("working-hours"),
			}},
		},
		{
			name: "a held lease names the work",
			status: model.EnvironmentIdleStatus{
				Leases: []model.EnvironmentActivityLease{
					{ID: "gradle-build", Name: "gradle-build", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []model.EnvironmentIdleMarker{activeMarker("lease")},
			},
			wantBusy:   true,
			wantDetail: "holding: gradle-build",
		},
		{
			name: "a lease wins over a generic marker",
			status: model.EnvironmentIdleStatus{
				Leases: []model.EnvironmentActivityLease{
					{ID: "agent-run", Name: "agent-run", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []model.EnvironmentIdleMarker{activeMarker(config.ActivityKindMCP)},
			},
			wantBusy:   true,
			wantDetail: "holding: agent-run",
		},
		{
			name: "sampled processes describe themselves",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker(records.ActivityKindProcess),
			}},
			wantBusy:   true,
			wantDetail: "running build or agent processes",
		},
		{
			name: "an agent over MCP is named",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker(config.ActivityKindMCP),
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

func TestEnvActivitySnapshotCopiesRatherThanAliases(t *testing.T) {
	key := uimodel.SelectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	service := newTestService(Deps{})
	service.activity = map[string]ActivityState{key: {Busy: true, Detail: "gradle-build"}}

	snapshot := service.ActivitySnapshot()
	if len(snapshot) != 1 || !snapshot[key].Busy {
		t.Fatalf("expected the snapshot to carry the observation, got %+v", snapshot)
	}
	service.activity[key] = ActivityState{}
	if !snapshot[key].Busy {
		t.Fatalf("expected the snapshot to be a copy, unaffected by a later mutation of the cache")
	}
}

func TestEnvActivitySnapshotNilWhenEmpty(t *testing.T) {
	service := newTestService(Deps{})
	if snapshot := service.ActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected a nil snapshot for an empty poller state, got %+v", snapshot)
	}
}

func TestResetEnvironmentActivityObservationsClearsTheCache(t *testing.T) {
	service := newTestService(Deps{})
	service.PublishActivity(uimodel.Selection{Tenant: "acme", Environment: "dev"}, ActivityState{Busy: true, Detail: "gradle-build"})

	service.ResetEnvironmentActivityObservations()

	if snapshot := service.ActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected no cached observations after reset, got %+v", snapshot)
	}
}

func TestResetEnvironmentActivityObservationsOnEmptyCacheIsANoop(t *testing.T) {
	service := newTestService(Deps{})
	service.ResetEnvironmentActivityObservations()
	if snapshot := service.ActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected no cached observations, got %+v", snapshot)
	}
}

func TestPublishActivityEmitsOnlyTransitions(t *testing.T) {
	emits := newCapturedEmits()
	service := newTestService(Deps{Emit: emits.fn()})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}

	service.PublishActivity(selection, ActivityState{Reachable: true, Observed: true})
	service.PublishActivity(selection, ActivityState{Reachable: true, Observed: true})
	service.PublishActivity(selection, ActivityState{Reachable: true, Observed: true, Busy: true})

	if got := len(emits.events(uimodel.EnvActivityEvent)); got != 2 {
		t.Fatalf("a restated observation must not re-emit: got %d emits, want 2", got)
	}
}

func TestTriggerEnvironmentActivitySweepRunsSynchronously(t *testing.T) {
	store := fakeStore{
		tenants: map[string]model.TenantConfig{"acme": {Name: "acme"}},
		envs:    map[string]config.EnvConfig{"acme/dev": {Name: "dev"}},
	}
	emits := newCapturedEmits()
	service := newTestService(Deps{Store: store, Emit: emits.fn()})

	service.TriggerEnvironmentActivitySweep()

	if len(emits.events(uimodel.EnvActivityEvent)) == 0 {
		t.Fatal("triggering a sweep must observe and emit for every configured selection before returning")
	}
}
