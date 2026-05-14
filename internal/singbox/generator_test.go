package singbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"singboxA/internal/config"
)

func TestGenerateRouteUsesOnlyLocalRuleSets(t *testing.T) {
	dataDir := t.TempDir()
	writeRuleSetFile(t, dataDir, "chnroutes-bgp")
	writeRuleSetFile(t, dataDir, "geosite-cn")

	route := NewConfigGenerator(dataDir).generateRoute(config.AppState{ProxyMode: "rule"}, testGeneratorConfig())
	if len(route.RuleSet) != 2 {
		t.Fatalf("expected 2 local rule sets, got %d", len(route.RuleSet))
	}

	ruleSetDir := filepath.Join(dataDir, "singbox") + string(os.PathSeparator)
	for _, ruleSet := range route.RuleSet {
		if ruleSet.Type != "local" {
			t.Fatalf("expected local rule-set type, got %q for %s", ruleSet.Type, ruleSet.Tag)
		}
		if ruleSet.URL != "" || ruleSet.DownloadDetour != "" || ruleSet.UpdateInterval != "" {
			t.Fatalf("rule-set %s still contains remote update fields: %+v", ruleSet.Tag, ruleSet)
		}
		if !strings.HasPrefix(ruleSet.Path, ruleSetDir) {
			t.Fatalf("rule-set %s path %q is not under %q", ruleSet.Tag, ruleSet.Path, ruleSetDir)
		}
	}

	if !routeReferencesRuleSet(route, "chnroutes-bgp") {
		t.Fatalf("expected route rules to reference chnroutes-bgp")
	}
	if !routeReferencesRuleSet(route, "geosite-cn") {
		t.Fatalf("expected route rules to reference geosite-cn")
	}
}

func TestGenerateRouteSkipsMissingRuleSets(t *testing.T) {
	route := NewConfigGenerator(t.TempDir()).generateRoute(config.AppState{
		ProxyMode: "rule",
		CustomRules: []config.CustomRule{
			{Type: "geosite", Value: "cn", Outbound: "direct"},
		},
	}, testGeneratorConfig())

	if len(route.RuleSet) != 0 {
		t.Fatalf("expected no rule_set entries when local cache is empty, got %+v", route.RuleSet)
	}
	for _, rule := range route.Rules {
		if len(rule.RuleSet) > 0 {
			t.Fatalf("expected missing rule-set references to be skipped, got rule %+v", rule)
		}
	}
}

func TestGenerateDNSSkipsMissingRuleSets(t *testing.T) {
	dns := NewConfigGenerator(t.TempDir()).generateDNS(testGeneratorConfig())
	for _, rule := range dns.Rules {
		if len(rule.RuleSet) > 0 {
			t.Fatalf("expected DNS rule-set references to be skipped when local cache is empty, got rule %+v", rule)
		}
	}
}

func testGeneratorConfig() config.Config {
	return config.Config{
		SingBox: config.SingBoxConfig{
			LogLevel: "info",
		},
		DNS: config.DNSConfig{
			DomesticServers: []string{"223.5.5.5"},
			ProxyServers:    []string{"1.1.1.1"},
		},
		Proxy: config.ProxyConfig{
			SOCK5Port:     10808,
			HTTPProxyPort: 10809,
		},
	}
}

func writeRuleSetFile(t *testing.T, dataDir, tag string) string {
	t.Helper()
	for _, spec := range builtInRuleSetFiles {
		if spec.Tag != tag {
			continue
		}
		path := ruleSetPath(dataDir, spec)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create rule-set dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("test-rule-set"), 0644); err != nil {
			t.Fatalf("failed to write rule-set file: %v", err)
		}
		return path
	}
	t.Fatalf("unknown rule-set tag %q", tag)
	return ""
}

func routeReferencesRuleSet(route *RouteConfig, tag string) bool {
	for _, rule := range route.Rules {
		for _, ruleSet := range rule.RuleSet {
			if ruleSet == tag {
				return true
			}
		}
	}
	return false
}
