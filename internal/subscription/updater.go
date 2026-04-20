package subscription

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"singboxA/internal/config"
	"singboxA/internal/nodeselector"
	"singboxA/internal/singbox"

	"gopkg.in/yaml.v3"
)

// Constants
const (
	HTTPTimeout    = 30 * time.Second
	CacheDirPerm   = 0755
	CacheFilePerm  = 0600
	ClashUserAgent = "clash-verge/v2.2.0"
)

type Updater struct {
	mu          sync.Mutex
	dataDir     string
	parser      *Parser
	converter   *Converter
	httpClient  *http.Client
	nodes       []singbox.Outbound
	nodeSources map[string][]string
	nodesMu     sync.RWMutex
	lastUpdate  time.Time
	stopChan    chan struct{}
	running     bool
	onRefresh   func(RefreshResult)
}

type CachedSubscription struct {
	ID        string       `yaml:"id"`
	Name      string       `yaml:"name"`
	URL       string       `yaml:"url"`
	UpdatedAt time.Time    `yaml:"updated_at"`
	Proxies   []ClashProxy `yaml:"proxies"`
}

type RefreshResult struct {
	BeforeNodes []singbox.Outbound
	AfterNodes  []singbox.Outbound
	Updated     bool
	Automatic   bool
}

var (
	updaterInstance *Updater
	updaterOnce     sync.Once
)

func GetUpdater() *Updater {
	updaterOnce.Do(func() {
		updaterInstance = &Updater{
			parser:    NewParser(),
			converter: NewConverter(),
			httpClient: &http.Client{
				Timeout: HTTPTimeout,
			},
			nodes:       make([]singbox.Outbound, 0),
			nodeSources: make(map[string][]string),
		}
	})
	return updaterInstance
}

func (u *Updater) Initialize(dataDir string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.dataDir = dataDir

	// Load cached nodes
	if err := u.loadCachedNodes(); err != nil {
		log.Printf("Warning: failed to load cached nodes: %v", err)
	}

	return nil
}

func (u *Updater) StartAutoUpdate(interval time.Duration) {
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return
	}
	u.running = true
	u.stopChan = make(chan struct{})
	stopChan := u.stopChan // Capture for goroutine
	u.mu.Unlock()

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				result, err := u.RefreshDueSubscriptions()
				if err != nil {
					log.Printf("Auto-update failed: %v", err)
				}
				if err == nil && result.Updated {
					u.mu.Lock()
					onRefresh := u.onRefresh
					u.mu.Unlock()
					if onRefresh != nil {
						onRefresh(result)
					}
				}
			case <-stopChan:
				return
			}
		}
	}()
}

func (u *Updater) StopAutoUpdate() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.running && u.stopChan != nil {
		close(u.stopChan)
		u.stopChan = nil
		u.running = false
	}
}

func (u *Updater) SetRefreshCallback(callback func(RefreshResult)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.onRefresh = callback
}

func (u *Updater) FetchSubscription(sub config.Subscription) ([]ClashProxy, error) {
	// Validate URL
	if _, err := url.ParseRequestURI(sub.URL); err != nil {
		return nil, fmt.Errorf("invalid subscription URL: %w", err)
	}

	req, err := http.NewRequest("GET", sub.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set user agent to avoid blocking
	req.Header.Set("User-Agent", ClashUserAgent)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	proxies, err := u.parser.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subscription: %w", err)
	}

	return proxies, nil
}

func (u *Updater) RefreshSubscription(sub config.Subscription) error {
	proxies, err := u.FetchSubscription(sub)
	if err != nil {
		return err
	}

	// Cache the subscription
	cached := CachedSubscription{
		ID:        sub.ID,
		Name:      sub.Name,
		URL:       sub.URL,
		UpdatedAt: time.Now(),
		Proxies:   proxies,
	}

	if err := u.saveCachedSubscription(cached); err != nil {
		return fmt.Errorf("failed to cache subscription: %w", err)
	}

	// Update state
	cfgMgr := config.GetManager()
	sub.UpdatedAt = time.Now().Format(time.RFC3339)
	cfgMgr.UpdateSubscription(sub)

	// Rebuild nodes
	return u.rebuildNodes()
}

func (u *Updater) RefreshAll() (RefreshResult, error) {
	cfgMgr := config.GetManager()
	subs := cfgMgr.GetSubscriptions()
	beforeNodes := u.GetNodes()

	if err := cfgMgr.ClearNodeTestResults(); err != nil {
		return RefreshResult{}, fmt.Errorf("failed to clear node test results: %w", err)
	}

	var lastErr error
	refreshed := false
	for _, sub := range subs {
		if err := u.RefreshSubscription(sub); err != nil {
			lastErr = err
			log.Printf("Failed to refresh subscription %s: %v", sub.Name, err)
			continue
		}
		refreshed = true
	}

	if refreshed {
		u.lastUpdate = time.Now()
	}
	return RefreshResult{
		BeforeNodes: beforeNodes,
		AfterNodes:  u.GetNodes(),
		Updated:     refreshed,
		Automatic:   false,
	}, lastErr
}

func (u *Updater) RefreshDueSubscriptions() (RefreshResult, error) {
	cfgMgr := config.GetManager()
	subs := cfgMgr.GetSubscriptions()
	dueSubs := make([]config.Subscription, 0, len(subs))
	beforeNodes := u.GetNodes()

	for _, sub := range subs {
		if u.shouldRefreshSubscription(sub) {
			dueSubs = append(dueSubs, sub)
		}
	}

	if len(dueSubs) == 0 {
		return RefreshResult{BeforeNodes: beforeNodes, AfterNodes: beforeNodes, Updated: false, Automatic: true}, nil
	}

	if err := cfgMgr.ClearNodeTestResults(); err != nil {
		return RefreshResult{}, fmt.Errorf("failed to clear node test results: %w", err)
	}

	var lastErr error
	refreshed := false
	for _, sub := range dueSubs {
		if err := u.RefreshSubscription(sub); err != nil {
			lastErr = err
			log.Printf("Failed to auto-refresh subscription %s: %v", sub.Name, err)
			continue
		}
		refreshed = true
	}

	if refreshed {
		u.lastUpdate = time.Now()
	}
	return RefreshResult{
		BeforeNodes: beforeNodes,
		AfterNodes:  u.GetNodes(),
		Updated:     refreshed,
		Automatic:   true,
	}, lastErr
}

func (u *Updater) shouldRefreshSubscription(sub config.Subscription) bool {
	if !sub.AutoUpdate {
		return false
	}

	intervalMinutes := sub.UpdateInterval
	if intervalMinutes <= 0 {
		return false
	}

	if sub.UpdatedAt == "" {
		return true
	}

	updatedAt, err := time.Parse(time.RFC3339, sub.UpdatedAt)
	if err != nil {
		return true
	}

	return time.Since(updatedAt) >= time.Duration(intervalMinutes)*time.Minute
}

func (u *Updater) GetNodes() []singbox.Outbound {
	u.nodesMu.RLock()
	defer u.nodesMu.RUnlock()

	result := make([]singbox.Outbound, len(u.nodes))
	copy(result, u.nodes)
	return result
}

func (u *Updater) GetLastUpdate() time.Time {
	return u.lastUpdate
}

func (u *Updater) GetNodeSources() map[string][]string {
	u.nodesMu.RLock()
	defer u.nodesMu.RUnlock()

	result := make(map[string][]string, len(u.nodeSources))
	for key, value := range u.nodeSources {
		result[key] = append([]string(nil), value...)
	}
	return result
}

func (u *Updater) saveCachedSubscription(cached CachedSubscription) error {
	cacheDir := filepath.Join(u.dataDir, "subscriptions")
	if err := os.MkdirAll(cacheDir, CacheDirPerm); err != nil {
		return err
	}

	filename := u.getSubscriptionFilename(cached.ID)
	data, err := yaml.Marshal(cached)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(cacheDir, filename), data, CacheFilePerm)
}

func (u *Updater) loadCachedNodes() error {
	cacheDir := filepath.Join(u.dataDir, "subscriptions")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var allProxies []ClashProxy
	nodeSources := make(map[string][]string)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if err != nil {
			continue
		}

		var cached CachedSubscription
		if err := yaml.Unmarshal(data, &cached); err != nil {
			continue
		}

		for _, proxy := range cached.Proxies {
			allProxies = append(allProxies, proxy)
			sourceKey := proxySourceKey(proxy)
			if sourceKey == "" {
				continue
			}
			if !containsSourceName(nodeSources[sourceKey], cached.Name) {
				nodeSources[sourceKey] = append(nodeSources[sourceKey], cached.Name)
			}
		}
	}

	converted := u.converter.Convert(allProxies)
	resolvedSources := make(map[string][]string, len(converted))
	for _, node := range converted {
		sourceKey := outboundSourceKey(node)
		if sourceKey == "" {
			continue
		}
		if sources, ok := nodeSources[sourceKey]; ok {
			resolvedSources[nodeselector.NodeKey(node)] = append([]string(nil), sources...)
		}
	}

	u.nodesMu.Lock()
	u.nodes = converted
	u.nodeSources = resolvedSources
	u.nodesMu.Unlock()

	return nil
}

func (u *Updater) rebuildNodes() error {
	return u.loadCachedNodes()
}

func (u *Updater) getSubscriptionFilename(id string) string {
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:8]) + ".yaml"
}

func (u *Updater) DeleteSubscriptionCache(id string) error {
	cacheDir := filepath.Join(u.dataDir, "subscriptions")
	filename := u.getSubscriptionFilename(id)

	err := os.Remove(filepath.Join(cacheDir, filename))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return u.rebuildNodes()
}

// GenerateID generates a cryptographically secure unique ID
func GenerateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based hash if crypto/rand fails
		hash := sha256.Sum256([]byte(time.Now().String()))
		return hex.EncodeToString(hash[:8])
	}
	return hex.EncodeToString(b[:8])
}

func proxySourceKey(proxy ClashProxy) string {
	if proxy.Name == "" || proxy.Type == "" || proxy.Server == "" || proxy.Port <= 0 {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%d", proxy.Name, proxy.Type, proxy.Server, proxy.Port)
}

func outboundSourceKey(node singbox.Outbound) string {
	if node.Tag == "" || node.Type == "" || node.Server == "" || node.ServerPort <= 0 {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%d", node.Tag, node.Type, node.Server, node.ServerPort)
}

func containsSourceName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
