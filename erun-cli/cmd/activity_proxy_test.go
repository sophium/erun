package cmd

import (
	"net"
	"sort"
	"testing"
)

// TestSSHActivityRecorderAggregatesPerClientBytes covers the proxy
// recorder's per-peer accounting that landed with the SSH client-IP
// surfacing work. The recorder's save path must surface one
// EnvironmentActivityClientUpdate per address that contributed bytes;
// addresses that contributed nothing are dropped before save.
func TestClientUpdatesFromMapDropsZeroAndNegative(t *testing.T) {
	updates := clientUpdatesFromMap(map[string]int64{
		"10.0.4.7":  1500,
		"10.0.4.8":  0,
		"127.0.0.1": 200,
		"10.0.4.9":  -8,
	})
	sort.Slice(updates, func(i, j int) bool { return updates[i].Address < updates[j].Address })
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %+v", updates)
	}
	if updates[0].Address != "10.0.4.7" || updates[0].Bytes != 1500 {
		t.Fatalf("unexpected first update: %+v", updates[0])
	}
	if updates[1].Address != "127.0.0.1" || updates[1].Bytes != 200 {
		t.Fatalf("unexpected second update: %+v", updates[1])
	}
}

func TestClientUpdatesFromMapReturnsNilForEmpty(t *testing.T) {
	if got := clientUpdatesFromMap(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
	if got := clientUpdatesFromMap(map[string]int64{}); got != nil {
		t.Fatalf("expected nil for empty map, got %+v", got)
	}
}

func TestExtractRemoteHostStripsPort(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{"ipv4", "10.0.4.7:42153", "10.0.4.7"},
		{"ipv6", "[::1]:42153", "::1"},
		{"no_port", "10.0.4.7", "10.0.4.7"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRemoteHost(stubAddr(tt.raw))
			if got != tt.want {
				t.Fatalf("extractRemoteHost(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExtractRemoteHostHandlesNilAndEmpty(t *testing.T) {
	if got := extractRemoteHost(nil); got != "" {
		t.Fatalf("nil addr should yield empty, got %q", got)
	}
	if got := extractRemoteHost(stubAddr("")); got != "" {
		t.Fatalf("empty addr should yield empty, got %q", got)
	}
}

type stubAddr string

func (s stubAddr) Network() string { return "tcp" }
func (s stubAddr) String() string  { return string(s) }

var _ net.Addr = stubAddr("")
