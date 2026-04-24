package singbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"singboxA/internal/config"
)

const (
	TesterStateStopped  = "stopped"
	TesterStateStarting = "starting"
	TesterStateRunning  = "running"
	TesterStateError    = "error"
)

type TestCoreStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type testWorker struct {
	index          int
	configPath      string
	controllerAddr  string
	secret          string
	configVersion   string
	cmd             *exec.Cmd
	cancel          context.CancelFunc
}

type TestCoreManager struct {
	mu          sync.RWMutex
	initialized bool
	binaryPath  string
	dataDir     string
	state       string
	lastError   string
	workers     []testWorker
	nextWorker  uint64
}

var (
	testCoreInstance *TestCoreManager
	testCoreOnce     sync.Once
)

func GetTestCoreManager() *TestCoreManager {
	testCoreOnce.Do(func() {
		testCoreInstance = &TestCoreManager{
			state: TesterStateStopped,
		}
	})
	return testCoreInstance
}

func (m *TestCoreManager) Initialize(binaryPath, dataDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.binaryPath = binaryPath
	m.dataDir = dataDir

	configDir := filepath.Join(dataDir, "singbox")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		m.state = TesterStateError
		m.lastError = err.Error()
		return err
	}

	for i := range m.workers {
		if m.workers[i].secret == "" {
			secret, err := randomSecret()
			if err != nil {
				m.state = TesterStateError
				m.lastError = err.Error()
				return err
			}
			m.workers[i].secret = secret
		}
		m.workers[i].index = i
		m.workers[i].configPath = filepath.Join(configDir, fmt.Sprintf("tester-config-%d.json", i+1))
	}

	m.ensureWorkerCountLocked(3)

	m.initialized = true
	return nil
}

func (m *TestCoreManager) Status() TestCoreStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return TestCoreStatus{
		State: m.state,
		Error: m.lastError,
	}
}

func (m *TestCoreManager) EnsureReady(nodes []Outbound, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		return fmt.Errorf("test core manager is not initialized")
	}

	realNodes := filterTestableNodes(nodes)
	if len(realNodes) == 0 {
		return m.stopLocked()
	}

	m.ensureWorkerCountLocked(normalizeTestWorkerCount(cfg.Proxy.TestWorkers))

	payloads, versions, err := m.buildWorkerPayloads(realNodes, cfg)
	if err != nil {
		m.state = TesterStateError
		m.lastError = err.Error()
		return err
	}

	for i := range m.workers {
		worker := &m.workers[i]
		needRestart := versions[i] != worker.configVersion || worker.cmd == nil || worker.cmd.Process == nil
		if !needRestart {
			continue
		}
		m.state = TesterStateStarting

		if err := os.WriteFile(worker.configPath, payloads[i], 0600); err != nil {
			m.state = TesterStateError
			m.lastError = err.Error()
			return err
		}
		if err := m.restartWorkerLocked(worker, versions[i]); err != nil {
			m.state = TesterStateError
			m.lastError = err.Error()
			return err
		}
		worker.configVersion = versions[i]
	}

	m.lastError = ""
	m.state = TesterStateRunning
	return nil
}

func (m *TestCoreManager) TestProxyDelay(tag, targetURL string, timeout time.Duration) (int, error) {
	m.mu.RLock()
	if m.state != TesterStateRunning || len(m.workers) == 0 {
		m.mu.RUnlock()
		return -1, fmt.Errorf("tester is not running")
	}

	workers := make([]testWorker, 0, len(m.workers))
	for _, worker := range m.workers {
		if worker.controllerAddr == "" || worker.cmd == nil || worker.cmd.Process == nil {
			continue
		}
		workers = append(workers, worker)
	}
	m.mu.RUnlock()

	if len(workers) == 0 {
		return -1, fmt.Errorf("no active tester workers")
	}
	if targetURL == "" {
		return -1, fmt.Errorf("test url is required")
	}

	start := int(atomic.AddUint64(&m.nextWorker, 1)-1) % len(workers)
	var lastErr error
	for i := 0; i < len(workers); i++ {
		worker := workers[(start+i)%len(workers)]
		delay, err := m.callDelayAPI(worker, tag, targetURL, timeout)
		if err == nil {
			m.clearError()
			return delay, nil
		}
		lastErr = err
	}

	m.setError(lastErr)
	return -1, lastErr
}

func (m *TestCoreManager) callDelayAPI(worker testWorker, tag, targetURL string, timeout time.Duration) (int, error) {
	apiURL := fmt.Sprintf("http://%s/proxies/%s/delay?url=%s&timeout=%d", worker.controllerAddr, url.PathEscape(tag), url.QueryEscape(targetURL), timeout.Milliseconds())
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("Authorization", "Bearer "+worker.secret)

	client := &http.Client{Timeout: timeout + 1500*time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return -1, fmt.Errorf("delay api returned %s: %s", resp.Status, string(body))
	}

	var payload struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return -1, err
	}
	return payload.Delay, nil
}

func (m *TestCoreManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *TestCoreManager) restartWorkerLocked(worker *testWorker, version string) error {
	controllerAddr := worker.controllerAddr
	if err := m.stopWorkerLocked(worker); err != nil {
		return err
	}
	worker.controllerAddr = controllerAddr

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.binaryPath, "run", "-c", worker.configPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start tester sing-box worker %d: %w", worker.index+1, err)
	}

	worker.cmd = cmd
	worker.cancel = cancel
	go m.waitWorkerProcess(worker.configPath, cmd, version)

	if err := m.waitForWorkerReadyLocked(worker, 6*time.Second); err != nil {
		_ = m.stopWorkerLocked(worker)
		return err
	}

	return nil
}

func (m *TestCoreManager) waitWorkerProcess(configPath string, cmd *exec.Cmd, version string) {
	err := cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.workers {
		worker := &m.workers[i]
		if worker.configPath != configPath {
			continue
		}
		if worker.cmd == cmd {
			worker.cmd = nil
			worker.cancel = nil
			if err != nil && worker.configVersion == version {
				m.state = TesterStateError
				m.lastError = fmt.Sprintf("tester worker %d exited: %v", i+1, err)
			}
		}
		return
	}
}

func (m *TestCoreManager) waitForWorkerReadyLocked(worker *testWorker, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if worker.cmd == nil || worker.cmd.Process == nil {
			return fmt.Errorf("tester worker %d exited before ready", worker.index+1)
		}
		if m.checkWorkerReadyLocked(*worker, 1200*time.Millisecond) {
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("tester worker %d api did not become ready", worker.index+1)
}

func (m *TestCoreManager) checkWorkerReadyLocked(worker testWorker, timeout time.Duration) bool {
	if worker.controllerAddr == "" {
		return false
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/proxies", worker.controllerAddr), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+worker.secret)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *TestCoreManager) stopLocked() error {
	for i := range m.workers {
		if err := m.stopWorkerLocked(&m.workers[i]); err != nil {
			return err
		}
		m.workers[i].configVersion = ""
		m.workers[i].controllerAddr = ""
	}
	if m.initialized {
		m.state = TesterStateStopped
	}
	return nil
}

func (m *TestCoreManager) ensureWorkerCountLocked(count int) {
	count = normalizeTestWorkerCount(count)
	current := len(m.workers)
	if current == count {
		return
	}

	if current > count {
		for i := count; i < current; i++ {
			_ = m.stopWorkerLocked(&m.workers[i])
		}
		m.workers = m.workers[:count]
	}

	if len(m.workers) < count {
		configDir := filepath.Join(m.dataDir, "singbox")
		for i := len(m.workers); i < count; i++ {
			secret, err := randomSecret()
			if err != nil {
				m.state = TesterStateError
				m.lastError = err.Error()
				return
			}
			m.workers = append(m.workers, testWorker{
				index:      i,
				configPath: filepath.Join(configDir, fmt.Sprintf("tester-config-%d.json", i+1)),
				secret:     secret,
			})
		}
	}

	for i := range m.workers {
		m.workers[i].index = i
		if m.workers[i].configPath == "" {
			m.workers[i].configPath = filepath.Join(m.dataDir, "singbox", fmt.Sprintf("tester-config-%d.json", i+1))
		}
		if m.workers[i].secret == "" {
			secret, err := randomSecret()
			if err != nil {
				m.state = TesterStateError
				m.lastError = err.Error()
				return
			}
			m.workers[i].secret = secret
		}
	}
}

func (m *TestCoreManager) stopWorkerLocked(worker *testWorker) error {
	if worker.cancel != nil {
		worker.cancel()
	}
	deadline := time.Now().Add(2 * time.Second)
	for worker.cmd != nil && time.Now().Before(deadline) {
		if worker.cmd.Process == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if worker.cmd != nil && worker.cmd.Process != nil {
		_ = worker.cmd.Process.Kill()
	}
	worker.cmd = nil
	worker.cancel = nil
	return nil
}

func (m *TestCoreManager) buildWorkerPayloads(nodes []Outbound, cfg config.Config) ([][]byte, []string, error) {
	payloads := make([][]byte, 0, len(m.workers))
	versions := make([]string, 0, len(m.workers))

	for i := range m.workers {
		if m.workers[i].controllerAddr == "" {
			controllerPort, err := reservePort()
			if err != nil {
				return nil, nil, err
			}
			m.workers[i].controllerAddr = fmt.Sprintf("127.0.0.1:%d", controllerPort)
		}

		payload, err := m.buildConfigPayload(nodes, cfg, m.workers[i])
		if err != nil {
			return nil, nil, err
		}
		payloads = append(payloads, payload)
		versions = append(versions, string(payload))
	}

	return payloads, versions, nil
}

func (m *TestCoreManager) buildConfigPayload(nodes []Outbound, cfg config.Config, worker testWorker) ([]byte, error) {
	dnsServer := "223.5.5.5"
	if len(cfg.DNS.DomesticServers) > 0 && cfg.DNS.DomesticServers[0] != "" {
		dnsServer = cfg.DNS.DomesticServers[0]
	}

	nodeTags := make([]string, 0, len(nodes))
	outbounds := make([]Outbound, 0, len(nodes)+3)
	for _, node := range nodes {
		outbounds = append(outbounds, node)
		nodeTags = append(nodeTags, node.Tag)
	}
	outbounds = append(outbounds,
		Outbound{
			Type:      "selector",
			Tag:       "proxy",
			Outbounds: append([]string(nil), nodeTags...),
			Default:   firstTag(nodeTags),
		},
		Outbound{
			Type:      "urltest",
			Tag:       "auto",
			Outbounds: append([]string(nil), nodeTags...),
			URL:       resolveTestURLForMode(cfg.Proxy.TestURLMode),
			Interval:  "10m",
			Tolerance: 50,
		},
		Outbound{Type: "direct", Tag: "direct"},
	)

	sbConfig := &SingBoxConfig{
		Log: &LogConfig{Level: "error"},
		DNS: &DNSConfig{
			Servers: []DNSServer{{
				Type:   "udp",
				Tag:    "test-dns",
				Server: dnsServer,
			}},
			Final:          "test-dns",
			Strategy:       "prefer_ipv4",
			Independent:    true,
			DisableExpire:  false,
			ReverseMapping: false,
		},
		Outbounds: outbounds,
		Route: &RouteConfig{
			Final:                 "direct",
			AutoDetectInterface:   true,
			DefaultDomainResolver: "test-dns",
		},
		Experimental: &Experimental{
			ClashAPI: &ClashAPI{
				ExternalController: worker.controllerAddr,
				Secret:             worker.secret,
			},
		},
	}

	return json.Marshal(sbConfig)
}

func (m *TestCoreManager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastError = err.Error()
		allRunning := true
		for _, worker := range m.workers {
			if worker.cmd == nil || worker.cmd.Process == nil {
				allRunning = false
				break
			}
		}
		if allRunning {
			m.state = TesterStateRunning
		} else {
			m.state = TesterStateError
		}
	}
}

func (m *TestCoreManager) clearError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = ""
	allRunning := true
	for _, worker := range m.workers {
		if worker.cmd == nil || worker.cmd.Process == nil {
			allRunning = false
			break
		}
	}
	if allRunning {
		m.state = TesterStateRunning
	}
}

func filterTestableNodes(nodes []Outbound) []Outbound {
	realNodes := make([]Outbound, 0, len(nodes))
	for _, node := range nodes {
		if node.Tag == "" || node.Server == "" || node.ServerPort <= 0 {
			continue
		}
		realNodes = append(realNodes, node)
	}
	return realNodes
}

func reservePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("failed to reserve port")
	}
	return addr.Port, nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func firstTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func resolveTestURLForMode(mode string) string {
	switch mode {
	case "youtube_ggpht":
		return "https://yt3.ggpht.com/favicon.ico"
	case "skk":
		return "https://latency-test.skk.moe/endpoint"
	case "jsdelivr":
		return "https://cdn.jsdelivr.net/npm/latency-test@1.0.0/generate_200"
	case "github":
		return "https://github.github.io/janky/images/bg_hr.png"
	default:
		return "https://www.gstatic.com/generate_204"
	}
}

func normalizeTestWorkerCount(count int) int {
	if count < 1 {
		return 1
	}
	if count > 5 {
		return 5
	}
	return count
}
