package protocol

import (
	"testing"
	"time"
)

func TestSupported(t *testing.T) {
	tests := []struct {
		version uint32
		want    bool
	}{
		{0, false},
		{MinSupported, true},
		{Version, true},
		{Version + 1, false},
	}
	for _, tt := range tests {
		if got := Supported(tt.version); got != tt.want {
			t.Errorf("Supported(%d) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestNegotiate(t *testing.T) {
	tests := []struct {
		min, max uint32
		want     uint32
		ok       bool
	}{
		{0, Version, Version, true},                // an agent that predates the min field
		{MinSupported, Version, Version, true},     // same range
		{MinSupported, Version + 3, Version, true}, // a newer agent that still speaks ours
		{0, 0, 0, false},
		{0, Version + 1, 0, false}, // a newer agent that speaks only newer versions
		{Version + 1, Version + 2, 0, false},
		{Version + 1, Version, Version, true}, // inverted range: treated as only max
	}
	for _, tt := range tests {
		got, ok := Negotiate(tt.min, tt.max)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("Negotiate(%d, %d) = %d, %v; want %d, %v", tt.min, tt.max, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNegotiateUpdateOnly(t *testing.T) {
	tests := []struct {
		min, max uint32
		want     uint32
		ok       bool
	}{
		{0, Version, Version, true},
		{UpdateContract, UpdateContract, UpdateContract, true}, // speaks only the oldest version: still updated
		{0, 0, 0, false},
		{0, Version + 1, 0, false}, // newer than the hub: update the hub instead
		{Version + 1, Version + 2, 0, false},
	}
	for _, tt := range tests {
		got, ok := NegotiateUpdateOnly(tt.min, tt.max)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("NegotiateUpdateOnly(%d, %d) = %d, %v; want %d, %v", tt.min, tt.max, got, ok, tt.want, tt.ok)
		}
	}
}

// The update contract must reach every agent that was ever released.
func TestTheUpdateContractIsNeverDropped(t *testing.T) {
	if UpdateContract != 1 {
		t.Errorf("UpdateContract = %d: agents that speak only protocol 1 could no longer be updated from the dashboard", UpdateContract)
	}
	if UpdateContract > MinSupported {
		t.Errorf("UpdateContract %d above MinSupported %d", UpdateContract, MinSupported)
	}
}

// TestKeepaliveEndsAgree: the hub's policy must allow the agent's pings, and a
// dead hub must be noticed within about 45 s (#222).
func TestKeepaliveEndsAgree(t *testing.T) {
	if KeepaliveMinTime > KeepaliveTime {
		t.Errorf("the hub refuses pings closer than %v but agents ping every %v", KeepaliveMinTime, KeepaliveTime)
	}
	if d := KeepaliveTime + KeepaliveTimeout; d > 45*time.Second {
		t.Errorf("a dead hub is noticed after up to %v, want 45 s at most", d)
	}
	if KeepaliveTime < 10*time.Second {
		t.Errorf("gRPC never pings more often than every 10 s; KeepaliveTime = %v", KeepaliveTime)
	}
}
