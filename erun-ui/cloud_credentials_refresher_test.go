package main

import (
	"testing"
	"time"
)

func TestNextCredentialRefreshDelayClampsToInterval(t *testing.T) {
	now := time.Now()
	delay := nextCredentialRefreshDelay(now.Add(2 * time.Hour))
	if delay != credentialRefreshInterval {
		t.Fatalf("expected delay clamped to %v, got %v", credentialRefreshInterval, delay)
	}
}

func TestNextCredentialRefreshDelayShortensForSoonExpiration(t *testing.T) {
	now := time.Now()
	expiration := now.Add(20 * time.Minute)
	delay := nextCredentialRefreshDelay(expiration)
	want := time.Until(expiration) - credentialRefreshLeadTime
	if delay > want+time.Second || delay < want-time.Second {
		t.Fatalf("expected delay ~%v for expiration in 20m, got %v", want, delay)
	}
}

func TestNextCredentialRefreshDelayUsesBackoffOnExpiredCreds(t *testing.T) {
	if got := nextCredentialRefreshDelay(time.Now().Add(-time.Hour)); got != credentialRefreshBackoff {
		t.Fatalf("expected expired credentials to schedule short backoff, got %v", got)
	}
}

func TestNextCredentialRefreshDelayUsesIntervalWhenExpirationUnknown(t *testing.T) {
	if got := nextCredentialRefreshDelay(time.Time{}); got != credentialRefreshInterval {
		t.Fatalf("expected default interval when expiration is zero, got %v", got)
	}
}
