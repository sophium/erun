package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestCloudContextCacheSmoothsATransientUnknown locks in the deliberate grace
// period: a single failed describe-instances call must not blank a
// known-good status the moment it comes in.
func TestCloudContextCacheSmoothsATransientUnknown(t *testing.T) {
	app := &App{}
	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx"}, Status: eruncommon.CloudContextStatusRunning},
	})
	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx"}, Status: eruncommon.CloudContextStatusUnknown},
	})
	if got := app.cloudContextStatus("ctx"); got != eruncommon.CloudContextStatusRunning {
		t.Fatalf("a single transient Unknown must not blank a known-good status: got %q", got)
	}
}

// TestCloudContextCacheHonoursUnknownPastTTL is the regression for erun#1216:
// a sustained AWS failure must downgrade the cache to Unknown rather than
// serve a "running" that may be an hour stale — the case that let the Stop
// button be offered against state the app could no longer verify.
func TestCloudContextCacheHonoursUnknownPastTTL(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: eruncommon.CloudContextStatusRunning, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx"}, Status: eruncommon.CloudContextStatusUnknown},
	})
	if got := app.cloudContextStatus("ctx"); got != eruncommon.CloudContextStatusUnknown {
		t.Fatalf("a stale known-good status must downgrade to Unknown once its confirmation TTL has passed: got %q", got)
	}
}

// TestCloudContextStatusReadSideAppliesTTLEvenWithoutANewPoll covers the case
// where the poller itself stops producing results entirely (e.g.
// RefreshCloudContextStatuses fails at the list-contexts step, before any
// per-context Unknown is even produced): the read path must age the
// observation out on its own, mirroring session_heartbeat.go's
// heartbeatSaysRunning.
func TestCloudContextStatusReadSideAppliesTTLEvenWithoutANewPoll(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: eruncommon.CloudContextStatusRunning, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	if got := app.cloudContextStatus("ctx"); got != eruncommon.CloudContextStatusUnknown {
		t.Fatalf("want Unknown for a stale observation with no fresh poll at all, got %q", got)
	}
}

// TestCloudContextCacheRecoversImmediatelyOnAGoodReading guards against
// over-correcting: once AWS answers again, the cache must reflect it right
// away rather than waiting out any grace period.
func TestCloudContextCacheRecoversImmediatelyOnAGoodReading(t *testing.T) {
	app := &App{
		cloudContextStatuses: map[string]cloudContextCacheEntry{
			"ctx": {status: eruncommon.CloudContextStatusUnknown, confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL)},
		},
	}
	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx"}, Status: eruncommon.CloudContextStatusRunning},
	})
	if got := app.cloudContextStatus("ctx"); got != eruncommon.CloudContextStatusRunning {
		t.Fatalf("a fresh good reading must apply immediately: got %q", got)
	}
}
