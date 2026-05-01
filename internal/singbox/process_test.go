package singbox

import (
	"path/filepath"
	"testing"
)

func TestPersistentLogsRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	pm := &ProcessManager{
		state:   StateStopped,
		logs:    make([]LogEntry, 0, DefaultMaxLogs),
		maxLogs: DefaultMaxLogs,
		logChan: make(chan LogEntry, LogChannelBuffer),
	}
	pm.Initialize("/bin/false", filepath.Join(tempDir, "singbox", "config.json"))

	pm.AddLog("info", "sing-box started")
	pm.AddLog("error", "sing-box exited: signal: killed\ncheck journal")

	logs, path, err := pm.GetPersistentLogs(1)
	if err != nil {
		t.Fatalf("GetPersistentLogs returned error: %v", err)
	}
	if want := filepath.Join(tempDir, "logs", "singbox.log"); path != want {
		t.Fatalf("log path = %q, want %q", path, want)
	}
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(logs))
	}
	if logs[0].Level != "error" {
		t.Fatalf("level = %q, want error", logs[0].Level)
	}
	if logs[0].Message != "sing-box exited: signal: killed\ncheck journal" {
		t.Fatalf("message = %q", logs[0].Message)
	}

	allLogs, _, err := pm.GetPersistentLogs(0)
	if err != nil {
		t.Fatalf("GetPersistentLogs all returned error: %v", err)
	}
	if len(allLogs) != 2 {
		t.Fatalf("got %d logs, want 2", len(allLogs))
	}
}
