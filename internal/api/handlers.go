package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"singboxA/internal/bypass"
	"singboxA/internal/config"
	"singboxA/internal/nodeselector"
	"singboxA/internal/rules"
	"singboxA/internal/singbox"
	"singboxA/internal/subscription"
)

type Handlers struct {
	cfgMgr       *config.Manager
	processMgr   *singbox.ProcessManager
	updater      *subscription.Updater
	generator    *singbox.ConfigGenerator
	rulesMgr     *rules.RuleManager
	bypassMgr    *bypass.Manager
	testMu       sync.Mutex
	testing      bool
	pendingRun   bool
	pendingNodes []singbox.Outbound
}

func NewHandlers() *Handlers {
	cfgMgr := config.GetManager()
	h := &Handlers{
		cfgMgr:     cfgMgr,
		processMgr: singbox.GetProcessManager(),
		updater:    subscription.GetUpdater(),
		generator:  singbox.NewConfigGenerator(cfgMgr.GetDataDir()),
		rulesMgr:   rules.NewRuleManager(),
		bypassMgr:  bypass.GetManager(),
	}
	h.updater.SetRefreshCallback(h.handleAutomaticRefresh)
	return h
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type ConfigPayload struct {
	config.Config
	NodeSelectionPreference string `json:"node_selection_preference"`
}

func (h *Handlers) sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true, Data: data})
}

func (h *Handlers) sendError(w http.ResponseWriter, status int, err string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{Success: false, Error: err})
}

// Status handlers

func (h *Handlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	status := h.processMgr.GetStatus()

	// Add additional info
	exists, version := h.processMgr.CheckBinary()
	nodes := h.updater.GetNodes()
	state := h.cfgMgr.GetState()
	resolved := nodeselector.Resolve(nodes, state)

	data := map[string]interface{}{
		"state":                     status.State,
		"pid":                       status.PID,
		"binary_exists":             exists,
		"version":                   strings.TrimSpace(version),
		"node_count":                len(nodes),
		"node_testing":              h.isNodeTesting(),
		"selected_node":             resolved.LogicalNode,
		"selected_mode":             resolved.SelectedMode,
		"effective_node":            resolved.EffectiveNode,
		"recommended_node":          resolved.Recommended,
		"node_selection_preference": resolved.Preference,
		"proxy_mode":                state.ProxyMode,
		"last_update":               h.updater.GetLastUpdate(),
	}

	h.sendJSON(w, data)
}

// Process control handlers

func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Generate config before starting
	if err := h.generateConfig(); err != nil {
		h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate config: %v", err))
		return
	}

	if err := h.processMgr.Start(); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Save auto-start state
	h.cfgMgr.SetAutoStart(true)

	h.sendJSON(w, map[string]string{"status": "started"})
}

func (h *Handlers) Stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if err := h.processMgr.Stop(); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Save auto-start state
	h.cfgMgr.SetAutoStart(false)

	h.sendJSON(w, map[string]string{"status": "stopped"})
}

func (h *Handlers) Restart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	nodes := h.updater.GetNodes()
	state := h.cfgMgr.GetState()
	if state.SelectedNode == nodeselector.AutoNodeTag {
		if err := h.applyRecommendedAutoSelection(nodes); err != nil {
			h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to apply recommended node before restart: %v", err))
			return
		}
	}

	// Generate config before restarting
	if err := h.generateConfig(); err != nil {
		h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate config: %v", err))
		return
	}

	if err := h.processMgr.Restart(); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.sendJSON(w, map[string]string{"status": "restarted"})
}

// Subscription handlers

func (h *Handlers) HandleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		subs := h.cfgMgr.GetSubscriptions()
		h.sendJSON(w, subs)
	case "POST":
		var sub config.Subscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if sub.URL == "" {
			h.sendError(w, http.StatusBadRequest, "URL is required")
			return
		}
		if sub.UpdateInterval < 0 {
			h.sendError(w, http.StatusBadRequest, "Update interval must be greater than or equal to 0")
			return
		}

		sub.ID = subscription.GenerateID()
		if sub.Name == "" {
			sub.Name = "Subscription " + sub.ID[:6]
		}

		if err := h.cfgMgr.AddSubscription(sub); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Fetch subscription in background
		go func() {
			if err := h.updater.RefreshSubscription(sub); err != nil {
				log.Printf("Failed to refresh subscription %s: %v", sub.Name, err)
				return
			}
			if _, err := h.normalizeSelectionState(); err != nil {
				log.Printf("Failed to normalize node selection after adding subscription: %v", err)
				return
			}
			h.queueBackgroundNodeTesting(h.updater.GetNodes())
		}()

		h.sendJSON(w, sub)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handlers) HandleSubscriptionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/subscriptions/")
	if id == "" {
		h.sendError(w, http.StatusBadRequest, "Subscription ID required")
		return
	}

	switch r.Method {
	case "PUT":
		var sub config.Subscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		sub.ID = id
		if sub.URL == "" {
			h.sendError(w, http.StatusBadRequest, "URL is required")
			return
		}
		if sub.UpdateInterval < 0 {
			h.sendError(w, http.StatusBadRequest, "Update interval must be greater than or equal to 0")
			return
		}

		if err := h.cfgMgr.UpdateSubscription(sub); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		h.sendJSON(w, sub)
	case "DELETE":
		if err := h.cfgMgr.DeleteSubscription(id); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if err := h.updater.DeleteSubscriptionCache(id); err != nil {
			// Log warning but don't fail - subscription is already deleted
			log.Printf("Warning: failed to delete subscription cache: %v", err)
		}
		selectionChanged, err := h.normalizeSelectionState()
		if err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if selectionChanged && h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := h.processMgr.Restart(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		h.sendJSON(w, map[string]string{"status": "deleted"})
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handlers) RefreshSubscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	result, err := h.updater.RefreshAll()
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.handleRefreshResult(result)

	h.sendJSON(w, map[string]string{
		"status":         "refreshed",
		"testing_status": "started",
	})
}

// Node handlers

type NodeInfo struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Selected      bool   `json:"selected"`
	Latency       int    `json:"latency,omitempty"`
	EffectiveNode string `json:"effective_node,omitempty"`
	Recommended   string `json:"recommended_node,omitempty"`
	Virtual       bool   `json:"virtual,omitempty"`
}

func (h *Handlers) GetNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	nodes := h.updater.GetNodes()
	state := h.cfgMgr.GetState()
	resolved := nodeselector.Resolve(nodes, state)
	testResults := h.cfgMgr.GetNodeTestResults()
	autoEffective := state.AppliedAutoNode
	autoRecommended := state.RecommendedAutoNode
	if autoRecommended == "" {
		autoRecommended = nodeselector.PickRecommendedNode(nodes, state.NodeSelectionPreference, state.NodeTestResults)
	}
	if autoEffective == "" || !nodeExistsByTag(nodes, autoEffective) {
		autoEffective = autoRecommended
	}

	nodeInfos := make([]NodeInfo, 0, len(nodes)+1)
	autoNode := NodeInfo{
		Name:          nodeselector.AutoNodeTag,
		Type:          "auto",
		Selected:      resolved.SelectedMode == "auto",
		EffectiveNode: autoEffective,
		Recommended:   autoRecommended,
		Virtual:       true,
	}
	for _, node := range nodes {
		if node.Tag != autoEffective {
			continue
		}
		if latency, ok := testResults[nodeselector.NodeKey(node)]; ok {
			autoNode.Latency = latency
		}
		break
	}
	nodeInfos = append(nodeInfos, autoNode)
	for _, node := range nodes {
		nodeInfo := NodeInfo{
			Name:     node.Tag,
			Type:     node.Type,
			Server:   node.Server,
			Port:     node.ServerPort,
			Selected: resolved.SelectedMode == "manual" && node.Tag == resolved.LogicalNode,
		}
		if latency, ok := testResults[nodeselector.NodeKey(node)]; ok {
			nodeInfo.Latency = latency
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}

	h.sendJSON(w, nodeInfos)
}

func (h *Handlers) HandleNodeAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		h.sendError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	nodeName := parts[0]
	action := parts[1]

	switch action {
	case "select":
		if r.Method != "POST" {
			h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}

		selectedNode := nodeName
		if selectedNode != nodeselector.AutoNodeTag {
			if !h.nodeExists(selectedNode) {
				h.sendError(w, http.StatusNotFound, "Node not found")
				return
			}
		}

		if err := h.cfgMgr.SetSelectedNode(selectedNode); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if selectedNode == nodeselector.AutoNodeTag {
			if err := h.ensureAutoSelectionLocked(h.updater.GetNodes()); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else {
			if err := h.resetAutoSelectionToRecommended(h.updater.GetNodes()); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// Regenerate config if running
		if h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			h.processMgr.Restart()
		}

		nodes := h.updater.GetNodes()
		resolved := nodeselector.Resolve(nodes, h.cfgMgr.GetState())
		h.sendJSON(w, map[string]string{
			"selected":         resolved.LogicalNode,
			"selected_mode":    resolved.SelectedMode,
			"effective_node":   resolved.EffectiveNode,
			"recommended_node": resolved.Recommended,
		})

	case "apply-recommended":
		if r.Method != "POST" {
			h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if nodeName != nodeselector.AutoNodeTag {
			h.sendError(w, http.StatusBadRequest, "Apply recommended is only supported for auto node")
			return
		}

		nodes := h.updater.GetNodes()
		if err := h.applyRecommendedAutoSelection(nodes); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := h.processMgr.Restart(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		resolved := nodeselector.Resolve(nodes, h.cfgMgr.GetState())
		h.sendJSON(w, map[string]string{
			"selected":         resolved.LogicalNode,
			"selected_mode":    resolved.SelectedMode,
			"effective_node":   resolved.EffectiveNode,
			"recommended_node": resolved.Recommended,
		})

	case "test":
		if r.Method != "POST" {
			h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}

		// Find node
		nodes := h.updater.GetNodes()
		var targetNode *singbox.Outbound
		for _, node := range nodes {
			if node.Tag == nodeName {
				targetNode = &node
				break
			}
		}

		if targetNode == nil {
			h.sendError(w, http.StatusNotFound, "Node not found")
			return
		}

		// Test latency
		latency, err := h.testNodeLatency(targetNode.Server, targetNode.ServerPort)
		if err != nil {
			_ = h.cfgMgr.SetNodeTestResult(nodeselector.NodeKey(*targetNode), -1)
			h.sendJSON(w, map[string]interface{}{
				"node":    nodeName,
				"latency": -1,
				"error":   err.Error(),
			})
			return
		}

		if err := h.cfgMgr.SetNodeTestResult(nodeselector.NodeKey(*targetNode), latency); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		h.sendJSON(w, map[string]interface{}{
			"node":    nodeName,
			"latency": latency,
		})

	default:
		h.sendError(w, http.StatusBadRequest, "Invalid action")
	}
}

func (h *Handlers) TestAllNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	nodes := h.updater.GetNodes()
	if len(nodes) == 0 {
		h.sendError(w, http.StatusBadRequest, "No nodes available")
		return
	}

	h.queueBackgroundNodeTesting(nodes)
	h.sendJSON(w, map[string]string{
		"status": "started",
	})
}

func (h *Handlers) testNodeLatency(server string, port int) (int, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", server, port), 5*time.Second)
	if err != nil {
		return -1, err
	}
	conn.Close()
	return int(time.Since(start).Milliseconds()), nil
}

// Config handlers

func (h *Handlers) HandleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		cfg := h.cfgMgr.GetConfig()
		h.sendJSON(w, ConfigPayload{
			Config:                  cfg,
			NodeSelectionPreference: nodeselector.NormalizePreference(h.cfgMgr.GetNodeSelectionPreference()),
		})
	case "PUT":
		var payload ConfigPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := h.cfgMgr.UpdateConfig(payload.Config); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.cfgMgr.SetNodeSelectionPreference(nodeselector.NormalizePreference(payload.NodeSelectionPreference)); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.ensureAutoSelectionLocked(h.updater.GetNodes()); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		selectionChanged, err := h.normalizeSelectionState()
		if err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if (selectionChanged || h.cfgMgr.GetState().SelectedNode == nodeselector.AutoNodeTag) && h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := h.processMgr.Restart(); err != nil {
				h.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		h.sendJSON(w, ConfigPayload{
			Config:                  payload.Config,
			NodeSelectionPreference: nodeselector.NormalizePreference(payload.NodeSelectionPreference),
		})
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// Rules handlers

func (h *Handlers) HandleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		rules := h.rulesMgr.GetCustomRules()
		defaultRules := h.rulesMgr.GetDefaultRules()
		geosites := h.rulesMgr.GetAvailableGeosites()
		geoips := h.rulesMgr.GetAvailableGeoips()

		h.sendJSON(w, map[string]interface{}{
			"custom_rules":     rules,
			"default_rules":    defaultRules,
			"geosite_values":   geosites,
			"geoip_values":     geoips,
			"last_rule_update": h.rulesMgr.GetLastRuleUpdate(),
		})
	case "PUT":
		var rules []config.CustomRule
		if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := h.rulesMgr.SetCustomRules(rules); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Regenerate config if running
		if h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate config: %v", err))
				return
			}
			if err := h.processMgr.Restart(); err != nil {
				h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart: %v", err))
				return
			}
		}

		h.sendJSON(w, rules)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handlers) RefreshRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Update last rule update time
	if err := h.rulesMgr.SetLastRuleUpdate(); err != nil {
		h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update rule time: %v", err))
		return
	}

	// Delete rule cache files to force re-download
	dataDir := h.cfgMgr.GetDataDir()
	ruleCacheDir := filepath.Join(dataDir, "singbox")
	if err := h.clearRuleCache(ruleCacheDir); err != nil {
		// Log but don't fail - cache clear is not critical
		fmt.Printf("Warning: failed to clear rule cache: %v\n", err)
	}

	// Regenerate config and restart if running
	if h.processMgr.GetState() == singbox.StateRunning {
		if err := h.generateConfig(); err != nil {
			h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate config: %v", err))
			return
		}
		if err := h.processMgr.Restart(); err != nil {
			h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart: %v", err))
			return
		}
	}

	h.sendJSON(w, map[string]interface{}{
		"success":          true,
		"last_rule_update": h.rulesMgr.GetLastRuleUpdate(),
	})
}

// clearRuleCache deletes .srs cache files in the given directory
func (h *Handlers) clearRuleCache(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".srs") {
			filePath := filepath.Join(dir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				return err
			}
			fmt.Printf("Deleted rule cache: %s\n", filePath)
		}
	}
	return nil
}

func (h *Handlers) HandleProxyMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		mode := h.rulesMgr.GetProxyMode()
		h.sendJSON(w, map[string]string{"mode": mode})
	case "PUT":
		var req struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := h.rulesMgr.SetProxyMode(req.Mode); err != nil {
			h.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Regenerate config if running
		if h.processMgr.GetState() == singbox.StateRunning {
			if err := h.generateConfig(); err != nil {
				h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate config: %v", err))
				return
			}
			if err := h.processMgr.Restart(); err != nil {
				h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart: %v", err))
				return
			}
		}

		h.sendJSON(w, map[string]string{"mode": req.Mode})
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// Logs handler - returns recent logs as JSON
func (h *Handlers) GetLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	logs := h.processMgr.GetLogs(100)
	h.sendJSON(w, logs)
}

// GetLogsSSE handles Server-Sent Events for real-time logs
func (h *Handlers) GetLogsSSE(w http.ResponseWriter, r *http.Request) {
	h.handleSSELogs(w, r)
}

// ClearLogs clears all stored logs
func (h *Handlers) ClearLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "DELETE" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	h.processMgr.ClearLogs()
	h.sendJSON(w, map[string]string{"status": "cleared"})
}

// SetLogLevel changes sing-box log level dynamically
func (h *Handlers) SetLogLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate log level
	validLevels := map[string]bool{
		"trace": true, "debug": true, "info": true,
		"warn": true, "error": true, "fatal": true, "panic": true,
	}
	if !validLevels[req.Level] {
		h.sendError(w, http.StatusBadRequest, "Invalid log level. Valid: trace, debug, info, warn, error, fatal, panic")
		return
	}

	// Update config
	cfg := h.cfgMgr.GetConfig()
	cfg.SingBox.LogLevel = req.Level
	if err := h.cfgMgr.UpdateConfig(cfg); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Need to restart sing-box for log level to take effect
	h.sendJSON(w, map[string]interface{}{
		"status":  "updated",
		"level":   req.Level,
		"message": "Restart sing-box to apply new log level",
	})
}

// GetLogLevel returns current log level setting
func (h *Handlers) GetLogLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	cfg := h.cfgMgr.GetConfig()
	h.sendJSON(w, map[string]string{"level": cfg.SingBox.LogLevel})
}

// HandleLogLevel handles GET and POST for log level
func (h *Handlers) HandleLogLevel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		h.GetLogLevel(w, r)
	case "POST":
		h.SetLogLevel(w, r)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handlers) handleSSELogs(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Restrict CORS to localhost
	origin := r.Header.Get("Origin")
	if origin == "" || strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to logs
	logChan := h.processMgr.SubscribeLogs()
	defer h.processMgr.UnsubscribeLogs(logChan)

	// Send existing logs first
	existingLogs := h.processMgr.GetLogs(100)
	for _, log := range existingLogs {
		data, err := json.Marshal(log)
		if err != nil {
			continue // Skip malformed entries
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	// Send new logs as they arrive
	ctx := r.Context()
	for {
		select {
		case log := <-logChan:
			data, err := json.Marshal(log)
			if err != nil {
				continue // Skip malformed entries
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// Cache handlers

func (h *Handlers) ClearCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	cfg := h.cfgMgr.GetConfig()
	cachePath := cfg.SingBox.ConfigPath
	// Cache file is in the same directory as config, named cache.db
	cacheDir := cachePath[:len(cachePath)-len("config.json")]
	cacheFile := cacheDir + "cache.db"

	// Stop sing-box first
	wasRunning := h.processMgr.GetStatus().State == "running"
	if wasRunning {
		if err := h.processMgr.Stop(); err != nil {
			h.sendError(w, http.StatusInternalServerError, "Failed to stop sing-box: "+err.Error())
			return
		}
	}

	// Delete cache file
	if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
		h.sendError(w, http.StatusInternalServerError, "Failed to delete cache: "+err.Error())
		return
	}

	// Restart if was running
	if wasRunning {
		if err := h.generateConfig(); err != nil {
			h.sendError(w, http.StatusInternalServerError, "Failed to generate config: "+err.Error())
			return
		}
		if err := h.processMgr.Start(); err != nil {
			h.sendError(w, http.StatusInternalServerError, "Failed to restart sing-box: "+err.Error())
			return
		}
	}

	h.sendJSON(w, map[string]string{"status": "cache_cleared"})
}

// Helper functions

func (h *Handlers) generateConfig() error {
	nodes := h.updater.GetNodes()
	if _, err := h.normalizeSelectionStateWithNodes(nodes); err != nil {
		return err
	}
	if err := h.ensureAutoSelectionLocked(nodes); err != nil {
		return err
	}
	cfg := h.cfgMgr.GetConfig()
	state := h.cfgMgr.GetState()

	sbConfig, err := h.generator.Generate(nodes, cfg, state)
	if err != nil {
		return err
	}

	return h.generator.SaveConfig(sbConfig, cfg.SingBox.ConfigPath)
}

func (h *Handlers) nodeExists(name string) bool {
	nodes := h.updater.GetNodes()
	for _, node := range nodes {
		if node.Tag == name {
			return true
		}
	}
	return false
}

func (h *Handlers) normalizeSelectionState() (bool, error) {
	return h.normalizeSelectionStateWithNodes(h.updater.GetNodes())
}

func (h *Handlers) normalizeSelectionStateWithNodes(nodes []singbox.Outbound) (bool, error) {
	state := h.cfgMgr.GetState()
	resolved := nodeselector.Resolve(nodes, state)
	changed := false

	normalizedPreference := nodeselector.NormalizePreference(state.NodeSelectionPreference)
	if normalizedPreference != state.NodeSelectionPreference {
		if err := h.cfgMgr.SetNodeSelectionPreference(normalizedPreference); err != nil {
			return false, err
		}
		changed = true
	}

	if state.SelectedNode != resolved.LogicalNode {
		if err := h.cfgMgr.SetSelectedNode(resolved.LogicalNode); err != nil {
			return false, err
		}
		changed = true
	}

	return changed, nil
}

func (h *Handlers) ensureAutoSelectionLocked(nodes []singbox.Outbound) error {
	state := h.cfgMgr.GetState()
	recommended := nodeselector.PickRecommendedNode(nodes, state.NodeSelectionPreference, state.NodeTestResults)
	applied := state.AppliedAutoNode
	if applied == "" || !nodeExistsByTag(nodes, applied) {
		applied = recommended
	}
	if state.AppliedAutoNode == applied && state.RecommendedAutoNode == recommended {
		return nil
	}
	return h.cfgMgr.SetAutoSelectionState(applied, recommended)
}

func (h *Handlers) resetAutoSelectionToRecommended(nodes []singbox.Outbound) error {
	state := h.cfgMgr.GetState()
	recommended := nodeselector.PickRecommendedNode(nodes, state.NodeSelectionPreference, state.NodeTestResults)
	if recommended == "" {
		recommended = state.AppliedAutoNode
	}
	if state.AppliedAutoNode == recommended && state.RecommendedAutoNode == recommended {
		return nil
	}
	return h.cfgMgr.SetAutoSelectionState(recommended, recommended)
}

func (h *Handlers) applyRecommendedAutoSelection(nodes []singbox.Outbound) error {
	state := h.cfgMgr.GetState()
	recommended := state.RecommendedAutoNode
	if recommended == "" || !nodeExistsByTag(nodes, recommended) {
		recommended = nodeselector.PickRecommendedNode(nodes, state.NodeSelectionPreference, state.NodeTestResults)
	}
	if recommended == "" {
		return fmt.Errorf("no recommended node available")
	}
	if state.AppliedAutoNode == recommended && state.RecommendedAutoNode == recommended {
		return nil
	}
	return h.cfgMgr.SetAutoSelectionState(recommended, recommended)
}

func (h *Handlers) handleAutomaticRefresh(result subscription.RefreshResult) {
	h.handleRefreshResult(result)
}

func (h *Handlers) handleRefreshResult(result subscription.RefreshResult) {
	if !result.Updated {
		return
	}

	beforeState := h.cfgMgr.GetState()
	beforeResolved := nodeselector.Resolve(result.BeforeNodes, beforeState)

	if _, err := h.normalizeSelectionStateWithNodes(result.AfterNodes); err != nil {
		log.Printf("Failed to normalize selection after subscription refresh: %v", err)
		return
	}

	afterState := h.cfgMgr.GetState()
	afterResolved := nodeselector.Resolve(result.AfterNodes, afterState)

	if h.processMgr.GetState() == singbox.StateRunning && h.shouldRestartAfterSubscriptionRefresh(result.BeforeNodes, result.AfterNodes, beforeResolved, afterResolved) {
		if err := h.generateConfig(); err != nil {
			log.Printf("Failed to generate config after subscription refresh: %v", err)
		} else if err := h.processMgr.Restart(); err != nil {
			log.Printf("Failed to restart sing-box after subscription refresh: %v", err)
		}
	}

	h.queueBackgroundNodeTesting(result.AfterNodes)
}

func (h *Handlers) shouldRestartAfterSubscriptionRefresh(
	beforeNodes []singbox.Outbound,
	afterNodes []singbox.Outbound,
	beforeResolved nodeselector.ResolvedSelection,
	afterResolved nodeselector.ResolvedSelection,
) bool {
	if beforeResolved.SelectedMode != afterResolved.SelectedMode {
		return true
	}

	if beforeResolved.LogicalNode != afterResolved.LogicalNode {
		return true
	}

	if afterResolved.SelectedMode != "manual" {
		return false
	}

	beforeNode, beforeOK := findNodeByTag(beforeNodes, beforeResolved.EffectiveNode)
	afterNode, afterOK := findNodeByTag(afterNodes, afterResolved.EffectiveNode)
	if beforeOK != afterOK {
		return true
	}
	if !beforeOK || !afterOK {
		return false
	}

	return !reflect.DeepEqual(beforeNode, afterNode)
}

func findNodeByTag(nodes []singbox.Outbound, tag string) (singbox.Outbound, bool) {
	for _, node := range nodes {
		if node.Tag == tag {
			return node, true
		}
	}
	return singbox.Outbound{}, false
}

func nodeExistsByTag(nodes []singbox.Outbound, tag string) bool {
	_, ok := findNodeByTag(nodes, tag)
	return ok
}

func (h *Handlers) isNodeTesting() bool {
	h.testMu.Lock()
	defer h.testMu.Unlock()
	return h.testing || h.pendingRun
}

func (h *Handlers) queueBackgroundNodeTesting(nodes []singbox.Outbound) {
	h.testMu.Lock()
	if h.testing {
		h.pendingRun = true
		h.pendingNodes = append([]singbox.Outbound(nil), nodes...)
		h.testMu.Unlock()
		return
	}
	h.testing = true
	h.testMu.Unlock()

	go func() {
		currentNodes := append([]singbox.Outbound(nil), nodes...)
		for {
			if len(currentNodes) > 0 {
				results := h.runNodeTests(currentNodes)
				if err := h.cfgMgr.ReplaceNodeTestResults(results); err != nil {
					log.Printf("Failed to persist node test results: %v", err)
				} else if err := h.reconcileAutoSelectionAfterTesting(currentNodes); err != nil {
					log.Printf("Failed to reconcile auto selection after testing: %v", err)
				}
				log.Printf("Background node testing completed for %d nodes", len(currentNodes))
			}

			h.testMu.Lock()
			if !h.pendingRun {
				h.testing = false
				h.testMu.Unlock()
				return
			}
			currentNodes = append([]singbox.Outbound(nil), h.pendingNodes...)
			h.pendingRun = false
			h.pendingNodes = nil
			h.testMu.Unlock()
		}
	}()
}

func (h *Handlers) runNodeTests(nodes []singbox.Outbound) map[string]int {
	results := make(map[string]int, len(nodes))
	const workerCount = 5
	nodeChan := make(chan singbox.Outbound)
	var wg sync.WaitGroup
	var resultsMu sync.Mutex

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for node := range nodeChan {
				latency, err := h.testNodeLatency(node.Server, node.ServerPort)
				if err != nil {
					latency = -1
				}
				resultsMu.Lock()
				results[nodeselector.NodeKey(node)] = latency
				resultsMu.Unlock()
			}
		}()
	}

	for _, node := range nodes {
		nodeChan <- node
	}
	close(nodeChan)
	wg.Wait()
	return results
}

func (h *Handlers) reconcileAutoSelectionAfterTesting(nodes []singbox.Outbound) error {
	state := h.cfgMgr.GetState()
	recommended := nodeselector.PickRecommendedNode(nodes, state.NodeSelectionPreference, state.NodeTestResults)
	applied := state.AppliedAutoNode
	switchTarget := ""

	if state.SelectedNode != nodeselector.AutoNodeTag {
		applied = recommended
	} else {
		switch {
		case applied == "":
			switchTarget = recommended
		case !nodeExistsByTag(nodes, applied):
			switchTarget = recommended
		default:
			node, ok := findNodeByTag(nodes, applied)
			if ok {
				if latency, exists := state.NodeTestResults[nodeselector.NodeKey(node)]; exists && latency < 0 && recommended != "" && recommended != applied {
					switchTarget = recommended
				}
			}
		}
	}

	if switchTarget != "" {
		applied = switchTarget
	} else if applied == "" {
		applied = recommended
	}

	if err := h.cfgMgr.SetAutoSelectionState(applied, recommended); err != nil {
		return err
	}

	if switchTarget == "" || h.processMgr.GetState() != singbox.StateRunning {
		return nil
	}

	if err := h.generateConfig(); err != nil {
		return err
	}
	return h.processMgr.Restart()
}

// Bypass handlers

// HandleBypass 处理绕过列表的 GET/POST/DELETE 请求
func (h *Handlers) HandleBypass(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		h.getBypassList(w, r)
	case "POST":
		h.addBypassEntry(w, r)
	case "DELETE":
		h.removeBypassEntry(w, r)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handlers) getBypassList(w http.ResponseWriter, r *http.Request) {
	list := h.bypassMgr.GetBypassList()
	gateway, iface := h.bypassMgr.GetGatewayInfo()

	h.sendJSON(w, map[string]interface{}{
		"bypass_list": list,
		"gateway":     gateway,
		"interface":   iface,
	})
}

func (h *Handlers) addBypassEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Comment string `json:"comment"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Address == "" {
		h.sendError(w, http.StatusBadRequest, "Address is required")
		return
	}

	if err := h.bypassMgr.AddBypassEntry(req.Address, req.Comment); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.sendJSON(w, map[string]string{
		"status":  "added",
		"address": req.Address,
	})
}

func (h *Handlers) removeBypassEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Address == "" {
		h.sendError(w, http.StatusBadRequest, "Address is required")
		return
	}

	if err := h.bypassMgr.RemoveBypassEntry(req.Address); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.sendJSON(w, map[string]string{
		"status":  "removed",
		"address": req.Address,
	})
}

// RefreshBypass 刷新绕过路由（域名 IP 可能变化）
func (h *Handlers) RefreshBypass(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if err := h.bypassMgr.RefreshRoutes(); err != nil {
		h.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.sendJSON(w, map[string]string{"status": "refreshed"})
}

// ProxyClash 代理 Clash API 请求（解决浏览器 CORS 限制）
func (h *Handlers) ProxyClash(w http.ResponseWriter, r *http.Request) {
	// 仅允许 GET 和 DELETE 方法
	if r.Method != "GET" && r.Method != "DELETE" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// 获取 clashAPI 地址
	clashAddr := "http://127.0.0.1:9091"

	// 从路径提取 target（如 /connections、/connections/{id}）
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/clash")
	if path == "" {
		path = "/"
	}

	// 构建目标 URL
	targetURL := clashAddr + path

	// 创建代理请求
	req, err := http.NewRequest(r.Method, targetURL, nil)
	if err != nil {
		h.sendError(w, http.StatusBadGateway, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	// 转发查询参数
	req.URL.RawQuery = r.URL.RawQuery

	// 发送请求
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.sendError(w, http.StatusBadGateway, fmt.Sprintf("Failed to connect to Clash API: %v", err))
		return
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.sendError(w, http.StatusBadGateway, fmt.Sprintf("Failed to read response: %v", err))
		return
	}

	// 设置响应头
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

	// 如果是 DELETE 请求，返回 JSON 响应
	if r.Method == "DELETE" {
		w.Header().Set("Content-Type", "application/json")
		h.sendJSON(w, map[string]string{"status": "ok"})
		return
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
