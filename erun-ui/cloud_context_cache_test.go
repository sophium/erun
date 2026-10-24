package main

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func TestCloudContextCacheSmoothsATransientUnknown(t *testing.T) {
	app := &App{}
	app.applyCloudContextStatusesToCache([]config.CloudContextStatus{
		{CloudContextConfig: config.CloudContextConfig{Name: "ctx"}, Status: cloud.CloudContextStatusRunning},
	})
	app.applyCloudContextStatusesToCache([]config.CloudContextStatus{
		{CloudContextConfig: config.CloudContextConfig{Name: "ctx"}, Status: cloud.CloudContextStatusUnknown},
	})
	if got := app.cloudContextStatus("ctx"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("a single transient Unknown must not blank a known-good status: got %q", got)
	}
}

func TestCloudContextCacheHonoursUnknownPastTTL(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: cloud.CloudContextStatusRunning, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	app.applyCloudContextStatusesToCache([]config.CloudContextStatus{
		{CloudContextConfig: config.CloudContextConfig{Name: "ctx"}, Status: cloud.CloudContextStatusUnknown},
	})
	if got := app.cloudContextStatus("ctx"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("a stale known-good status must downgrade to Unknown once its confirmation TTL has passed: got %q", got)
	}
}

// The read path must age out the observation even when the poller produces nothing.
func TestCloudContextStatusReadSideAppliesTTLEvenWithoutANewPoll(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: cloud.CloudContextStatusRunning, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	if got := app.cloudContextStatus("ctx"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("want Unknown for a stale observation with no fresh poll at all, got %q", got)
	}
}

func TestCloudContextCacheRecoversImmediatelyOnAGoodReading(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: cloud.CloudContextStatusUnknown, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	app.applyCloudContextStatusesToCache([]config.CloudContextStatus{
		{CloudContextConfig: config.CloudContextConfig{Name: "ctx"}, Status: cloud.CloudContextStatusRunning},
	})
	if got := app.cloudContextStatus("ctx"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("a fresh good reading must apply immediately: got %q", got)
	}
}
