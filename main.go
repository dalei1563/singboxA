package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"singboxA/internal/api"
	"singboxA/internal/bypass"
	"singboxA/internal/config"
	"singboxA/internal/singbox"
	"singboxA/internal/subscription"
)

//go:embed web/templates/* web/static/*
var webFS embed.FS

// Timeouts
const (
	ServerReadTimeout  = 15 * time.Second
	ServerWriteTimeout = 15 * time.Second
	ShutdownTimeout    = 10 * time.Second
)

func main() {
	// Determine data directory
	dataDir := os.Getenv("SINGBOX_DATA_DIR")
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to get home directory: %v", err)
		}
		dataDir = filepath.Join(homeDir, ".singboxA")
	}

	// Initialize configuration
	cfgMgr := config.GetManager()
	if err := cfgMgr.Initialize(dataDir); err != nil {
		log.Fatalf("Failed to initialize config: %v", err)
	}

	cfg := cfgMgr.GetConfig()

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Initialize subscription updater
	updater := subscription.GetUpdater()
	if err := updater.Initialize(dataDir); err != nil {
		log.Fatalf("Failed to initialize subscription updater: %v", err)
	}

	// Start auto-update if enabled
	if cfg.Subscription.AutoUpdate {
		interval := time.Duration(cfg.Subscription.UpdateInterval) * time.Minute
		updater.StartAutoUpdate(interval)
	}

	// Initialize process manager
	processMgr := singbox.GetProcessManager()
	processMgr.Initialize(cfg.SingBox.BinaryPath, cfg.SingBox.ConfigPath)

	// Initialize bypass manager
	bypassMgr := bypass.GetManager()
	if err := bypassMgr.Initialize(cfgMgr); err != nil {
		log.Printf("Warning: failed to initialize bypass manager: %v", err)
	} else {
		// Apply bypass routes
		if err := bypassMgr.ApplyBypassRoutes(); err != nil {
			log.Printf("Warning: failed to apply bypass routes: %v", err)
		}
		// Start auto-refresh every hour
		bypassMgr.StartAutoRefresh(1 * time.Hour)
	}

	// Auto-start sing-box if enabled
	state := cfgMgr.GetState()
	if state.AutoStart {
		log.Println("Auto-starting sing-box...")
		generator := singbox.NewConfigGenerator(cfgMgr.GetDataDir())
		nodes := updater.GetNodes()
		if len(nodes) > 0 {
			sbConfig, err := generator.Generate(nodes, cfg, state)
			if err != nil {
				log.Printf("Warning: failed to generate config for auto-start: %v", err)
			} else if err := generator.SaveConfig(sbConfig, cfg.SingBox.ConfigPath); err != nil {
				log.Printf("Warning: failed to save config for auto-start: %v", err)
			} else if err := processMgr.Start(); err != nil {
				log.Printf("Warning: failed to auto-start sing-box: %v", err)
			} else {
				log.Println("sing-box auto-started successfully")
			}
		} else {
			log.Println("No nodes available, skipping auto-start")
		}
	}

	// Pre-download rule files if not exist (for first-time installation)
	if err := ensureRuleFiles(cfgMgr.GetDataDir()); err != nil {
		log.Printf("Warning: failed to ensure rule files: %v", err)
	}

	// Create router
	router := api.NewRouter(webFS)

	// Create server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  ServerReadTimeout,
		WriteTimeout: ServerWriteTimeout,
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")

		// Stop auto-update
		updater.StopAutoUpdate()

		// Stop sing-box if running
		if processMgr.GetState() == singbox.StateRunning {
			if err := processMgr.Stop(); err != nil {
				log.Printf("Warning: failed to stop sing-box: %v", err)
			}
		}

		// Stop bypass auto-refresh
		bypassMgr.StopAutoRefresh()

		// Remove bypass routes
		if err := bypassMgr.RemoveBypassRoutes(); err != nil {
			log.Printf("Warning: failed to remove bypass routes: %v", err)
		}

		// Graceful shutdown with timeout
		ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Warning: server shutdown error: %v", err)
			server.Close()
		}
	}()

	// Start server
	log.Printf("SingBox Manager starting on http://%s", addr)
	log.Printf("Data directory: %s", dataDir)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped")
}

// validateConfig validates the configuration
func validateConfig(cfg config.Config) error {
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", cfg.Server.Port)
	}

	if cfg.SingBox.BinaryPath == "" {
		return fmt.Errorf("sing-box binary path is required")
	}

	if cfg.SingBox.ConfigPath == "" {
		return fmt.Errorf("sing-box config path is required")
	}

	if len(cfg.DNS.DomesticServers) == 0 {
		return fmt.Errorf("at least one domestic DNS server is required")
	}

	if len(cfg.DNS.ProxyServers) == 0 {
		return fmt.Errorf("at least one proxy DNS server is required")
	}

	return nil
}

// ensureRuleFiles downloads rule files if they don't exist
func ensureRuleFiles(dataDir string) error {
	singboxDir := filepath.Join(dataDir, "singbox")

	// Check if rule files already exist
	entries, err := os.ReadDir(singboxDir)
	if err != nil {
		return err
	}

	hasRuleFile := false
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".srs" {
			hasRuleFile = true
			break
		}
	}

	if hasRuleFile {
		return nil // Rule files exist, no need to download
	}

	log.Println("No rule files found, downloading...")

	ruleURLs := []string{
		"https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/chnroutes.txt.srs",
		"https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/accelerated-domains.china.conf.srs",
		"https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/apple.china.conf.srs",
		"https://testingcf.jsdelivr.net/gh/Dreista/sing-box-rule-set-cn@rule-set/google.china.conf.srs",
		"https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-cn.srs",
		"https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-geolocation-!cn.srs",
		"https://testingcf.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-category-ads-all.srs",
	}

	client := &http.Client{Timeout: 30 * time.Second}
	for _, url := range ruleURLs {
		filename := filepath.Base(url)
		filepath := filepath.Join(singboxDir, filename)

		if _, err := os.Stat(filepath); err == nil {
			continue // File exists
		}

		log.Printf("  Downloading %s...", filename)
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("  Warning: failed to download %s: %v", filename, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("  Warning: failed to download %s: HTTP %d", filename, resp.StatusCode)
			continue
		}

		out, err := os.Create(filepath)
		if err != nil {
			log.Printf("  Warning: failed to create %s: %v", filename, err)
			continue
		}

		if _, err := out.ReadFrom(resp.Body); err != nil {
			out.Close()
			os.Remove(filepath)
			log.Printf("  Warning: failed to save %s: %v", filename, err)
			continue
		}
		out.Close()
	}

	// Verify at least some files were downloaded
	entries, _ = os.ReadDir(singboxDir)
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".srs" {
			count++
		}
	}

	if count == 0 {
		return fmt.Errorf("failed to download any rule files")
	}

	log.Printf("Downloaded %d rule files", count)
	return nil
}
