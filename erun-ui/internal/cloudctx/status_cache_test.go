package cloudctx

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// testClock lets a test age cache entries without sleeping.
type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

func newClockedService(t *testing.T) (*Service, *testClock) {
	t.Helper()
	clock := &testClock{at: time.Now()}
	s := newTestService(t, fakeStore{}, nil)
	s.d.Now = clock.now
	return s, clock
}

func status(name, value string) model.CloudContextStatus {
	return model.CloudContextStatus{CloudContextConfig: model.CloudContextConfig{Name: name}, Status: value}
}

func TestCloudContextCacheSmoothsATransientUnknown(t *testing.T) {
	s, _ := newClockedService(t)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusRunning)})
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusUnknown)})
	if got := s.Status("ctx"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("a single transient Unknown must not blank a known-good status: got %q", got)
	}
}

func TestCloudContextCacheHonoursUnknownPastTTL(t *testing.T) {
	s, clock := newClockedService(t)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusRunning)})
	clock.at = clock.at.Add(2 * StatusTTL)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusUnknown)})
	if got := s.Status("ctx"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("a stale known-good status must downgrade to Unknown once its confirmation TTL has passed: got %q", got)
	}
}

// The read path must age out the observation even when the poller produces nothing.
func TestCloudContextStatusReadSideAppliesTTLEvenWithoutANewPoll(t *testing.T) {
	s, clock := newClockedService(t)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusRunning)})
	clock.at = clock.at.Add(2 * StatusTTL)
	if got := s.Status("ctx"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("want Unknown for a stale observation with no fresh poll at all, got %q", got)
	}
}

func TestCloudContextCacheRecoversImmediatelyOnAGoodReading(t *testing.T) {
	s, clock := newClockedService(t)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusUnknown)})
	clock.at = clock.at.Add(2 * StatusTTL)
	s.ApplyStatuses([]model.CloudContextStatus{status("ctx", cloud.CloudContextStatusRunning)})
	if got := s.Status("ctx"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("a fresh good reading must apply immediately: got %q", got)
	}
}

func TestApplyStatusesPreservesKnownStatusOnTransientUnknown(t *testing.T) {
	// A transient Unknown must not overwrite a known status; it only fills empty slots.
	s, _ := newClockedService(t)
	s.ApplyStatuses([]model.CloudContextStatus{
		status("ctx-a", cloud.CloudContextStatusRunning),
		status("ctx-b", cloud.CloudContextStatusStopped),
	})
	if got := s.Status("ctx-a"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("seed running, got %q", got)
	}
	if got := s.Status("ctx-b"); got != cloud.CloudContextStatusStopped {
		t.Fatalf("seed stopped, got %q", got)
	}

	transient := status("ctx-a", cloud.CloudContextStatusUnknown)
	transient.Message = "status refresh failed: token expired"
	s.ApplyStatuses([]model.CloudContextStatus{
		transient,
		status("ctx-b", cloud.CloudContextStatusRunning),
		status("ctx-c", cloud.CloudContextStatusUnknown),
	})
	if got := s.Status("ctx-a"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("transient unknown should not overwrite running, got %q", got)
	}
	if got := s.Status("ctx-b"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("authoritative transition stopped->running should overwrite, got %q", got)
	}
	if got := s.Status("ctx-c"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("first-observation unknown should populate empty slot, got %q", got)
	}
}

func TestSetStatusRepublishesAfterTheWrite(t *testing.T) {
	s, _ := newClockedService(t)
	var seen []string
	s.d.OnStatusChange = func() { seen = append(seen, s.Status("ctx")) }
	s.SetStatus("ctx", cloud.CloudContextStatusRunning)
	if len(seen) != 1 || seen[0] != cloud.CloudContextStatusRunning {
		t.Fatalf("a start/stop write must republish with the new status readable, got %v", seen)
	}
	s.SetStatus(" ", cloud.CloudContextStatusRunning)
	if len(seen) != 1 {
		t.Fatalf("a nameless write must not republish, got %v", seen)
	}
}
