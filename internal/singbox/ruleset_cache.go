package singbox

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const ruleSetDownloadTimeout = 30 * time.Second

type RuleSetFileSpec struct {
	Tag      string
	Filename string
	URL      string
}

type RuleSetRefreshResult struct {
	Updated []string          `json:"updated"`
	Kept    []string          `json:"kept"`
	Failed  map[string]string `json:"failed,omitempty"`
}

var builtInRuleSetFiles = []RuleSetFileSpec{
	{
		Tag:      "chnroutes-bgp",
		Filename: "chnroutes.txt.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/chnroutes.txt.srs",
	},
	{
		Tag:      "china-domains",
		Filename: "accelerated-domains.china.conf.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/accelerated-domains.china.conf.srs",
	},
	{
		Tag:      "apple-cn",
		Filename: "apple.china.conf.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/apple.china.conf.srs",
	},
	{
		Tag:      "google-cn",
		Filename: "google.china.conf.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/google.china.conf.srs",
	},
	{
		Tag:      "geosite-cn",
		Filename: "geosite-cn.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-cn.srs",
	},
	{
		Tag:      "geosite-geolocation-!cn",
		Filename: "geosite-geolocation-!cn.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-geolocation-!cn.srs",
	},
	{
		Tag:      "geosite-category-ads-all",
		Filename: "geosite-category-ads-all.srs",
		URL:      "https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-category-ads-all.srs",
	},
}

var ruleSetRefreshMu sync.Mutex

func BuiltInRuleSetFileSpecs() []RuleSetFileSpec {
	specs := make([]RuleSetFileSpec, len(builtInRuleSetFiles))
	copy(specs, builtInRuleSetFiles)
	return specs
}

func EnsureRuleSetFiles(dataDir string) (RuleSetRefreshResult, error) {
	return RefreshRuleSetFiles(dataDir, false)
}

func RefreshRuleSetFiles(dataDir string, force bool) (RuleSetRefreshResult, error) {
	ruleSetRefreshMu.Lock()
	defer ruleSetRefreshMu.Unlock()

	result := RuleSetRefreshResult{Failed: make(map[string]string)}
	cacheDir := ruleSetDir(dataDir)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return result, err
	}

	client := &http.Client{Timeout: ruleSetDownloadTimeout}
	for _, spec := range builtInRuleSetFiles {
		path := ruleSetPath(dataDir, spec)
		if !force && isUsableRuleSetFile(path) {
			result.Kept = append(result.Kept, spec.Tag)
			continue
		}

		if err := downloadRuleSetFile(client, spec.URL, path); err != nil {
			result.Failed[spec.Tag] = err.Error()
			if isUsableRuleSetFile(path) {
				result.Kept = append(result.Kept, spec.Tag)
			}
			continue
		}
		result.Updated = append(result.Updated, spec.Tag)
	}

	if len(result.Failed) == 0 {
		result.Failed = nil
	}
	if len(LocalRuleSets(dataDir)) == 0 {
		return result, fmt.Errorf("no usable rule-set files available")
	}
	return result, nil
}

func LocalRuleSets(dataDir string) []RuleSet {
	ruleSets := make([]RuleSet, 0, len(builtInRuleSetFiles))
	for _, spec := range builtInRuleSetFiles {
		path := ruleSetPath(dataDir, spec)
		if !isUsableRuleSetFile(path) {
			continue
		}
		ruleSets = append(ruleSets, RuleSet{
			Tag:    spec.Tag,
			Type:   "local",
			Format: "binary",
			Path:   path,
		})
	}
	return ruleSets
}

func LocalRuleSetTags(dataDir string) map[string]bool {
	tags := make(map[string]bool)
	for _, ruleSet := range LocalRuleSets(dataDir) {
		tags[ruleSet.Tag] = true
	}
	return tags
}

func ruleSetDir(dataDir string) string {
	return filepath.Join(dataDir, "singbox")
}

func ruleSetPath(dataDir string, spec RuleSetFileSpec) string {
	return filepath.Join(ruleSetDir(dataDir), spec.Filename)
}

func isUsableRuleSetFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func downloadRuleSetFile(client *http.Client, url, path string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	written, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 {
		return fmt.Errorf("empty rule-set file")
	}

	return os.Rename(tmpPath, path)
}
