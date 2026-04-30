package api

import (
	"testing"

	"singboxA/internal/nodeselector"
	"singboxA/internal/singbox"
)

func TestShouldRestartAfterSubscriptionRefreshAutoRestartsOnNodeChanges(t *testing.T) {
	h := &Handlers{}
	beforeNodes := []singbox.Outbound{
		{Tag: "node-a", Type: "trojan", Server: "old.example.com", ServerPort: 443},
		{Tag: "node-b", Type: "trojan", Server: "b.example.com", ServerPort: 443},
	}
	afterNodes := []singbox.Outbound{
		{Tag: "node-a", Type: "trojan", Server: "new.example.com", ServerPort: 443},
		{Tag: "node-b", Type: "trojan", Server: "b.example.com", ServerPort: 443},
	}
	beforeResolved := nodeselector.ResolvedSelection{
		SelectedMode:  "auto",
		LogicalNode:   "auto",
		EffectiveNode: "node-a",
		Recommended:   "node-b",
	}
	afterResolved := beforeResolved

	if !h.shouldRestartAfterSubscriptionRefresh(beforeNodes, afterNodes, beforeResolved, afterResolved) {
		t.Fatal("expected auto mode to restart when node definitions changed")
	}
}

func TestShouldRestartAfterSubscriptionRefreshAutoIgnoresRecommendationOnlyChange(t *testing.T) {
	h := &Handlers{}
	nodes := []singbox.Outbound{
		{Tag: "node-a", Type: "trojan", Server: "a.example.com", ServerPort: 443},
		{Tag: "node-b", Type: "trojan", Server: "b.example.com", ServerPort: 443},
	}
	beforeResolved := nodeselector.ResolvedSelection{
		SelectedMode:  "auto",
		LogicalNode:   "auto",
		EffectiveNode: "node-a",
		Recommended:   "node-a",
	}
	afterResolved := nodeselector.ResolvedSelection{
		SelectedMode:  "auto",
		LogicalNode:   "auto",
		EffectiveNode: "node-a",
		Recommended:   "node-b",
	}

	if h.shouldRestartAfterSubscriptionRefresh(nodes, nodes, beforeResolved, afterResolved) {
		t.Fatal("did not expect restart for recommendation-only change in stable auto mode")
	}
}

func TestIsUnhealthyLatency(t *testing.T) {
	cases := []struct {
		name    string
		latency int
		want    bool
	}{
		{name: "negative", latency: -1, want: true},
		{name: "timeout cap", latency: 999, want: true},
		{name: "usable", latency: 998, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnhealthyLatency(tc.latency); got != tc.want {
				t.Fatalf("isUnhealthyLatency(%d) = %v, want %v", tc.latency, got, tc.want)
			}
		})
	}
}
