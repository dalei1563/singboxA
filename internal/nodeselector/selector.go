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
	Preference    string `json:"node_selection_preference"`
}

func Resolve(nodes []singbox.Outbound, state config.AppState) ResolvedSelection {
	preference := NormalizePreference(state.NodeSelectionPreference)
	if preference == "" {
		preference = AutoNodeTag
	}

	if state.SelectedNode != "" && state.SelectedNode != AutoNodeTag && hasNode(nodes, state.SelectedNode) {
		return ResolvedSelection{
			SelectedMode:  "manual",
			LogicalNode:   state.SelectedNode,
			EffectiveNode: state.SelectedNode,
			Preference:    preference,
		}
	}

	return ResolvedSelection{
		SelectedMode:  "auto",
		LogicalNode:   AutoNodeTag,
		EffectiveNode: PickEffectiveNode(nodes, preference, state.NodeTestResults),
		Preference:    preference,
	}
}

func PickEffectiveNode(nodes []singbox.Outbound, preference string, testResults map[string]int) string {
	candidates := filterNodesByPreference(nodes, NormalizePreference(preference))
	if len(candidates) == 0 {
		candidates = append([]singbox.Outbound(nil), nodes...)
	}
	if len(candidates) == 0 {
		return ""
	}

	type rankedNode struct {
		node    singbox.Outbound
		latency int
		tested  bool
	}

	ranked := make([]rankedNode, 0, len(candidates))
	for _, node := range candidates {
		latency, ok := testResults[NodeKey(node)]
		ranked = append(ranked, rankedNode{
			node:    node,
			latency: latency,
			tested:  ok && latency >= 0,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].tested != ranked[j].tested {
			return ranked[i].tested
		}
		if ranked[i].tested && ranked[j].tested && ranked[i].latency != ranked[j].latency {
			return ranked[i].latency < ranked[j].latency
		}
		return ranked[i].node.Tag < ranked[j].node.Tag
	})

	return ranked[0].node.Tag
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
