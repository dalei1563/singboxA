package nodeselector

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"singboxA/internal/config"
	"singboxA/internal/singbox"
)

const AutoNodeTag = "auto"

type ResolvedSelection struct {
	SelectedMode  string `json:"selected_mode"`
	LogicalNode   string `json:"selected_node"`
	EffectiveNode string `json:"effective_node"`
	Recommended   string `json:"recommended_node"`
	Preference    string `json:"node_selection_preference"`
}

type rankedNode struct {
	node       singbox.Outbound
	latency    int
	tested     bool
	quality    config.NodeQualityResult
	hasQuality bool
}

func Resolve(nodes []singbox.Outbound, state config.AppState) ResolvedSelection {
	preference := NormalizePreference(state.NodeSelectionPreference)
	if preference == "" {
		preference = AutoNodeTag
	}
	recommended := PickRecommendedNode(nodes, preference, state.NodeTestResults, state.NodeQualityResults)

	if state.SelectedNode != "" && state.SelectedNode != AutoNodeTag && hasNode(nodes, state.SelectedNode) {
		return ResolvedSelection{
			SelectedMode:  "manual",
			LogicalNode:   state.SelectedNode,
			EffectiveNode: state.SelectedNode,
			Recommended:   recommended,
			Preference:    preference,
		}
	}

	effective := state.AppliedAutoNode
	if effective == "" {
		effective = recommended
	}

	return ResolvedSelection{
		SelectedMode:  "auto",
		LogicalNode:   AutoNodeTag,
		EffectiveNode: effective,
		Recommended:   recommended,
		Preference:    preference,
	}
}

func PickRecommendedNode(nodes []singbox.Outbound, preference string, testResults map[string]int, qualityResults map[string]config.NodeQualityResult) string {
	candidates := filterNodesByPreference(nodes, NormalizePreference(preference))
	if len(candidates) == 0 {
		candidates = append([]singbox.Outbound(nil), nodes...)
	}
	if len(candidates) == 0 {
		return ""
	}

	ranked := make([]rankedNode, 0, len(candidates))
	for _, node := range candidates {
		latency, ok := testResults[NodeKey(node)]
		quality, qualityOK := qualityResults[NodeKey(node)]
		ranked = append(ranked, rankedNode{
			node:       node,
			latency:    latency,
			tested:     ok && latency >= 0,
			quality:    quality,
			hasQuality: qualityOK && quality.TestedAt != "",
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		priorityI := recommendationPriority(ranked[i])
		priorityJ := recommendationPriority(ranked[j])
		if priorityI != priorityJ {
			return priorityI < priorityJ
		}
		if priorityI == 0 {
			if ranked[i].quality.SuccessRate != ranked[j].quality.SuccessRate {
				return ranked[i].quality.SuccessRate > ranked[j].quality.SuccessRate
			}
			latencyI := normalizeRecommendedLatency(ranked[i].quality.HTTPTTFB)
			latencyJ := normalizeRecommendedLatency(ranked[j].quality.HTTPTTFB)
			if latencyI != latencyJ {
				return latencyI < latencyJ
			}
		}
		if priorityI == 1 && ranked[i].latency != ranked[j].latency {
			return ranked[i].latency < ranked[j].latency
		}
		return ranked[i].node.Tag < ranked[j].node.Tag
	})

	return ranked[0].node.Tag
}

func normalizeRecommendedLatency(latency int) int {
	if latency < 0 {
		return latency
	}
	if latency > 999 {
		return 999
	}
	return latency
}

func recommendationPriority(node rankedNode) int {
	switch {
	case node.hasQuality && node.quality.SuccessCount > 0:
		return 0
	case node.tested:
		return 1
	case node.hasQuality:
		return 2
	default:
		return 3
	}
}

func NormalizePreference(preference string) string {
	switch strings.ToLower(strings.TrimSpace(preference)) {
	case "", AutoNodeTag:
		return AutoNodeTag
	case "us":
		return "us"
	case "sg":
		return "sg"
	case "tw":
		return "tw"
	case "hk":
		return "hk"
	default:
		return AutoNodeTag
	}
}

func NodeKey(node singbox.Outbound) string {
	return node.Tag + "|" + node.Server + "|" + strconv.Itoa(node.ServerPort)
}

func hasNode(nodes []singbox.Outbound, tag string) bool {
	for _, node := range nodes {
		if node.Tag == tag {
			return true
		}
	}
	return false
}

func filterNodesByPreference(nodes []singbox.Outbound, preference string) []singbox.Outbound {
	if preference == "" || preference == AutoNodeTag {
		return append([]singbox.Outbound(nil), nodes...)
	}

	filtered := make([]singbox.Outbound, 0, len(nodes))
	for _, node := range nodes {
		if matchesPreference(node.Tag, preference) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func matchesPreference(name, preference string) bool {
	keywords := map[string][]string{
		"us": {"美国", "美", "us", "usa", "unitedstates", "america"},
		"sg": {"新加坡", "狮城", "sg", "singapore"},
		"tw": {"台湾", "台灣", "tw", "taiwan"},
		"hk": {"香港", "hk", "hongkong"},
	}

	normalizedName := normalizeName(name)
	for _, keyword := range keywords[preference] {
		if strings.Contains(normalizedName, normalizeName(keyword)) {
			return true
		}
	}
	return false
}

func normalizeName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Han, r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
