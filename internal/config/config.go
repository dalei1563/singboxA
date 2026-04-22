package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Errors
var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
)

// File permissions
const (
	DirPerm  = 0755
	FilePerm = 0600 // Restrictive permissions for config files
)

type Config struct {
	Server       ServerConfig       `yaml:"server" json:"server"`
	SingBox      SingBoxConfig      `yaml:"singbox" json:"singbox"`
	DNS          DNSConfig          `yaml:"dns" json:"dns"`
	Proxy        ProxyConfig        `yaml:"proxy" json:"proxy"`
	Subscription SubscriptionConfig `yaml:"subscription" json:"subscription"`
}

type ServerConfig struct {
	Port int    `yaml:"port" json:"port"`
	Host string `yaml:"host" json:"host"`
}

type SingBoxConfig struct {
	BinaryPath string `yaml:"binary_path" json:"binary_path"`
	ConfigPath string `yaml:"config_path" json:"config_path"`
	LogLevel   string `yaml:"log_level" json:"log_level"`
}

type DNSConfig struct {
	DomesticServers []string `yaml:"domestic_servers" json:"domestic_servers"`
	ProxyServers    []string `yaml:"proxy_servers" json:"proxy_servers"`
}

type ProxyConfig struct {
	TUNEnabled    bool   `yaml:"tun_enabled" json:"tun_enabled"`         // 是否启用 TUN 模式
	TUNAddress    string `yaml:"tun_address" json:"tun_address"`         // TUN 接口地址
	TUNStack      string `yaml:"tun_stack" json:"tun_stack"`             // TUN 协议栈
	AutoRoute     bool   `yaml:"auto_route" json:"auto_route"`           // 自动路由
	StrictRoute   bool   `yaml:"strict_route" json:"strict_route"`       // 严格路由
	SOCK5Port     int    `yaml:"socks5_port" json:"socks5_port"`         // SOCKS5 代理端口，0 表示禁用
	HTTPProxyPort int    `yaml:"http_proxy_port" json:"http_proxy_port"` // HTTP 代理端口，0 表示禁用
	TestURLMode   string `yaml:"test_url_mode" json:"test_url_mode"`     // gstatic, youtube_ggpht, skk, jsdelivr, github
}

type SubscriptionConfig struct {
	AutoUpdate     bool `yaml:"auto_update" json:"auto_update"`
	UpdateInterval int  `yaml:"update_interval" json:"update_interval"` // in minutes
}

type Subscription struct {
	ID                   string `yaml:"id" json:"id"`
	Name                 string `yaml:"name" json:"name"`
	URL                  string `yaml:"url" json:"url"`
	UpdatedAt            string `yaml:"updated_at" json:"updated_at"`
	AutoUpdate           bool   `yaml:"auto_update" json:"auto_update"`
	AutoUpdateConfigured bool   `yaml:"auto_update_configured,omitempty" json:"-"`
	UpdateInterval       int    `yaml:"update_interval" json:"update_interval"`
}

type NodeQualityResult struct {
	TCPLatency   int    `yaml:"tcp_latency" json:"tcp_latency"`
	HTTPTTFB     int    `yaml:"http_ttfb" json:"http_ttfb"`
	HTTPTotal    int    `yaml:"http_total" json:"http_total"`
	SuccessRate  int    `yaml:"success_rate" json:"success_rate"`
	SuccessCount int    `yaml:"success_count" json:"success_count"`
	SampleCount  int    `yaml:"sample_count" json:"sample_count"`
	Score        int    `yaml:"score" json:"score"`
	TestedAt     string `yaml:"tested_at" json:"tested_at"`
}

type AppState struct {
	Subscriptions           []Subscription               `yaml:"subscriptions" json:"subscriptions"`
	SelectedNode            string                       `yaml:"selected_node" json:"selected_node"`
	AppliedAutoNode         string                       `yaml:"applied_auto_node" json:"applied_auto_node"`
	RecommendedAutoNode     string                       `yaml:"recommended_auto_node" json:"recommended_auto_node"`
	ProxyMode               string                       `yaml:"proxy_mode" json:"proxy_mode"` // global, rule, direct
	CustomRules             []CustomRule                 `yaml:"custom_rules" json:"custom_rules"`
	BypassList              []BypassEntry                `yaml:"bypass_list" json:"bypass_list"`           // 完全绕过 TUN 的地址
	AutoStart               bool                         `yaml:"auto_start" json:"auto_start"`             // 启动时自动启动 sing-box
	LastRuleUpdate          string                       `yaml:"last_rule_update" json:"last_rule_update"` // 上次规则更新时间
	NodeSelectionPreference string                       `yaml:"node_selection_preference" json:"node_selection_preference"`
	NodeTestResults         map[string]int               `yaml:"node_test_results" json:"-"`
	NodeQualityResults      map[string]NodeQualityResult `yaml:"node_quality_results" json:"-"`
}

// BypassEntry 表示一个需要完全绕过 TUN 的地址
type BypassEntry struct {
	Address string `yaml:"address" json:"address"` // 域名或 IP
	Comment string `yaml:"comment" json:"comment"` // 备注
}

type CustomRule struct {
	Type     string `yaml:"type" json:"type"` // domain, domain_suffix, ip_cidr, geosite, geoip
	Value    string `yaml:"value" json:"value"`
	Outbound string `yaml:"outbound" json:"outbound"` // proxy, direct, block
}

var (
	instance *Manager
	once     sync.Once
)

type Manager struct {
	mu      sync.RWMutex
	config  *Config
	state   *AppState
	dataDir string
}

func GetManager() *Manager {
	once.Do(func() {
		instance = &Manager{}
	})
	return instance
}

func (m *Manager) Initialize(dataDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.dataDir = dataDir

	// Ensure data directories exist
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, "subscriptions"),
		filepath.Join(dataDir, "singbox"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, DirPerm); err != nil {
			return err
		}
	}

	// Load or create default config
	if err := m.loadConfig(); err != nil {
		return err
	}

	// Load or create default state
	if err := m.loadState(); err != nil {
		return err
	}

	return nil
}

func (m *Manager) loadConfig() error {
	configPath := filepath.Join(m.dataDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.config = m.defaultConfig()
			return m.saveConfig()
		}
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	switch cfg.Proxy.TestURLMode {
	case "", "gstatic", "youtube_ggpht", "skk", "jsdelivr", "github":
	default:
		cfg.Proxy.TestURLMode = "gstatic"
	}
	if cfg.Proxy.TestURLMode == "" {
		cfg.Proxy.TestURLMode = "gstatic"
	}
	m.config = &cfg
	return nil
}

func (m *Manager) loadState() error {
	statePath := filepath.Join(m.dataDir, "state.yaml")

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			m.state = m.defaultState()
			return m.saveState()
		}
		return err
	}

	var state AppState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.SelectedNode == "" {
		state.SelectedNode = "auto"
	}
	if state.NodeSelectionPreference == "" {
		state.NodeSelectionPreference = "auto"
	}
	if state.NodeTestResults == nil {
		state.NodeTestResults = make(map[string]int)
	}
	if state.NodeQualityResults == nil {
		state.NodeQualityResults = make(map[string]NodeQualityResult)
	}
	m.normalizeSubscriptionsLocked(&state)
	m.state = &state
	return nil
}

func (m *Manager) defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 3333,
			Host: "0.0.0.0",
		},
		SingBox: SingBoxConfig{
			BinaryPath: "/usr/local/bin/sing-box",
			ConfigPath: filepath.Join(m.dataDir, "singbox", "config.json"),
			LogLevel:   "info",
		},
		DNS: DNSConfig{
			DomesticServers: []string{"223.5.5.5", "119.29.29.29"},
			ProxyServers:    []string{"8.8.8.8", "1.1.1.1"},
		},
		Proxy: ProxyConfig{
			TUNEnabled:    false, // 默认关闭 TUN 模式
			TUNAddress:    "172.19.0.1/30",
			TUNStack:      "system",
			AutoRoute:     true,
			StrictRoute:   true,
			SOCK5Port:     10808, // SOCKS5 端口
			HTTPProxyPort: 10809, // HTTP 代理端口
			TestURLMode:   "gstatic",
		},
		Subscription: SubscriptionConfig{
			AutoUpdate:     true,
			UpdateInterval: 60, // 1 hour
		},
	}
}

func (m *Manager) defaultState() *AppState {
	return &AppState{
		Subscriptions:           []Subscription{},
		SelectedNode:            "auto",
		AppliedAutoNode:         "",
		RecommendedAutoNode:     "",
		ProxyMode:               "rule",
		CustomRules:             []CustomRule{},
		BypassList:              []BypassEntry{},
		NodeSelectionPreference: "auto",
		NodeTestResults:         make(map[string]int),
		NodeQualityResults:      make(map[string]NodeQualityResult),
	}
}

func (m *Manager) normalizeSubscriptionsLocked(state *AppState) {
	defaultInterval := 60
	if m.config != nil && m.config.Subscription.UpdateInterval > 0 {
		defaultInterval = m.config.Subscription.UpdateInterval
	}

	for i := range state.Subscriptions {
		if !state.Subscriptions[i].AutoUpdateConfigured {
			state.Subscriptions[i].AutoUpdate = true
			state.Subscriptions[i].AutoUpdateConfigured = true
		}
		if state.Subscriptions[i].UpdateInterval <= 0 {
			state.Subscriptions[i].UpdateInterval = defaultInterval
		}
	}
}

func (m *Manager) saveConfig() error {
	configPath := filepath.Join(m.dataDir, "config.yaml")
	data, err := yaml.Marshal(m.config)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, FilePerm)
}

func (m *Manager) saveState() error {
	statePath := filepath.Join(m.dataDir, "state.yaml")
	data, err := yaml.Marshal(m.state)
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, FilePerm)
}

func (m *Manager) GetConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
}

func (m *Manager) UpdateConfig(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = &cfg
	return m.saveConfig()
}

func (m *Manager) GetState() AppState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.state
}

func (m *Manager) GetSubscriptions() []Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Subscriptions
}

func (m *Manager) AddSubscription(sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !sub.AutoUpdate && sub.UpdateInterval > 0 {
		sub.AutoUpdate = true
	}
	sub.AutoUpdateConfigured = true
	if sub.AutoUpdate && sub.UpdateInterval <= 0 {
		sub.UpdateInterval = m.config.Subscription.UpdateInterval
	}
	m.state.Subscriptions = append(m.state.Subscriptions, sub)
	return m.saveState()
}

func (m *Manager) UpdateSubscription(sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.state.Subscriptions {
		if s.ID == sub.ID {
			if !sub.AutoUpdate && sub.UpdateInterval > 0 && !s.AutoUpdate {
				sub.AutoUpdate = false
			} else if !sub.AutoUpdate && sub.UpdateInterval > 0 {
				sub.AutoUpdate = true
			}
			sub.AutoUpdateConfigured = true
			if sub.AutoUpdate && sub.UpdateInterval <= 0 {
				sub.UpdateInterval = m.config.Subscription.UpdateInterval
			}
			if sub.UpdatedAt == "" {
				sub.UpdatedAt = s.UpdatedAt
			}
			m.state.Subscriptions[i] = sub
			return m.saveState()
		}
	}
	return ErrSubscriptionNotFound
}

func (m *Manager) DeleteSubscription(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.state.Subscriptions {
		if s.ID == id {
			m.state.Subscriptions = append(m.state.Subscriptions[:i], m.state.Subscriptions[i+1:]...)
			return m.saveState()
		}
	}
	return ErrSubscriptionNotFound
}

func (m *Manager) SetSelectedNode(node string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.SelectedNode = node
	return m.saveState()
}

func (m *Manager) SetNodeSelectionPreference(preference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.NodeSelectionPreference = preference
	return m.saveState()
}

func (m *Manager) SetAutoSelectionState(appliedNode, recommendedNode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.AppliedAutoNode = appliedNode
	m.state.RecommendedAutoNode = recommendedNode
	return m.saveState()
}

func (m *Manager) SetAppliedAutoNode(node string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.AppliedAutoNode = node
	return m.saveState()
}

func (m *Manager) SetRecommendedAutoNode(node string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.RecommendedAutoNode = node
	return m.saveState()
}

func (m *Manager) GetNodeSelectionPreference() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.NodeSelectionPreference
}

func (m *Manager) GetNodeTestResults() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]int, len(m.state.NodeTestResults))
	for key, value := range m.state.NodeTestResults {
		results[key] = value
	}
	return results
}

func (m *Manager) GetNodeQualityResults() map[string]NodeQualityResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]NodeQualityResult, len(m.state.NodeQualityResults))
	for key, value := range m.state.NodeQualityResults {
		results[key] = value
	}
	return results
}

func (m *Manager) SetNodeTestResult(key string, latency int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state.NodeTestResults == nil {
		m.state.NodeTestResults = make(map[string]int)
	}
	m.state.NodeTestResults[key] = latency
	return m.saveState()
}

func (m *Manager) ReplaceNodeTestResults(results map[string]int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.NodeTestResults = make(map[string]int, len(results))
	for key, value := range results {
		m.state.NodeTestResults[key] = value
	}
	return m.saveState()
}

func (m *Manager) SetNodeQualityResult(key string, result NodeQualityResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state.NodeQualityResults == nil {
		m.state.NodeQualityResults = make(map[string]NodeQualityResult)
	}
	m.state.NodeQualityResults[key] = result
	return m.saveState()
}

func (m *Manager) ReplaceNodeQualityResults(results map[string]NodeQualityResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.NodeQualityResults = make(map[string]NodeQualityResult, len(results))
	for key, value := range results {
		m.state.NodeQualityResults[key] = value
	}
	return m.saveState()
}

func (m *Manager) ClearNodeTestResults() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.NodeTestResults = make(map[string]int)
	m.state.NodeQualityResults = make(map[string]NodeQualityResult)
	return m.saveState()
}

func (m *Manager) SetProxyMode(mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.ProxyMode = mode
	return m.saveState()
}

func (m *Manager) SetLastRuleUpdate() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.LastRuleUpdate = time.Now().Format("2006-01-02 15:04:05")
	return m.saveState()
}

func (m *Manager) GetLastRuleUpdate() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.LastRuleUpdate
}

func (m *Manager) GetCustomRules() []CustomRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.CustomRules
}

func (m *Manager) SetCustomRules(rules []CustomRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.CustomRules = rules
	return m.saveState()
}

func (m *Manager) GetDataDir() string {
	return m.dataDir
}

func (m *Manager) GetBypassList() []BypassEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.BypassList
}

func (m *Manager) SetBypassList(list []BypassEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.BypassList = list
	return m.saveState()
}

func (m *Manager) SetAutoStart(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.AutoStart = enabled
	return m.saveState()
}
