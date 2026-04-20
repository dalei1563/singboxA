package nodeselector

import (
	"testing"

	"singboxA/internal/config"
	"singboxA/internal/singbox"
)

func TestPickRecommendedNodePrefersQualityResults(t *testing.T) {
	nodes := []singbox.Outbound{
		{Tag: "🇸🇬 新加坡A", Server: "sg-a.example.com", ServerPort: 443},
		{Tag: "🇸🇬 新加坡B", Server: "sg-b.example.com", ServerPort: 443},
	}

	tcpResults := map[string]int{
		NodeKey(nodes[0]): 80,
		NodeKey(nodes[1]): 30,
	}
	qualityResults := map[string]config.NodeQualityResult{
		NodeKey(nodes[0]): {
			TCPLatency:   80,
			HTTPTTFB:     220,
			HTTPTotal:    480,
			SuccessRate:  100,
			SuccessCount: 3,
			SampleCount:  3,
			Score:        999,
			TestedAt:     "2026-04-20T00:00:00Z",
		},
	}

	got := PickRecommendedNode(nodes, "sg", tcpResults, qualityResults)
	if got != "🇸🇬 新加坡A" {
		t.Fatalf("expected quality-tested node to win, got %q", got)
	}
}

func TestPickRecommendedNodeFallsBackToTCPLatency(t *testing.T) {
	nodes := []singbox.Outbound{
		{Tag: "🇸🇬 新加坡A", Server: "sg-a.example.com", ServerPort: 443},
		{Tag: "🇸🇬 新加坡B", Server: "sg-b.example.com", ServerPort: 443},
	}

	tcpResults := map[string]int{
		NodeKey(nodes[0]): 80,
		NodeKey(nodes[1]): 30,
	}

	got := PickRecommendedNode(nodes, "sg", tcpResults, nil)
	if got != "🇸🇬 新加坡B" {
		t.Fatalf("expected lower tcp latency node, got %q", got)
	}
}
