package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"singboxA/internal/singbox"
)

func TestParseLogStreamOptionsSinceTime(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/logs/stream?include=all&limit=7&sinceTime=2026-05-06T15:30:12%2B08:00", nil)
	options, err := parseLogStreamOptions(req)
	if err != nil {
		t.Fatalf("parseLogStreamOptions returned error: %v", err)
	}
	if !options.IncludeAll {
		t.Fatal("expected include=all to enable full log streaming")
	}
	if !options.HasSinceTime {
		t.Fatal("expected sinceTime to be set")
	}
	if options.HistoryLimit != 7 {
		t.Fatalf("history limit = %d, want 7", options.HistoryLimit)
	}
	if got := options.SinceTime.Format(time.RFC3339); got != "2026-05-06T15:30:12+08:00" {
		t.Fatalf("sinceTime = %s", got)
	}
}

func TestParseLogStreamOptionsSinceID(t *testing.T) {
	ts := time.Date(2026, 5, 6, 15, 30, 12, 123, time.FixedZone("CST", 8*60*60))
	req := httptest.NewRequest("GET", "/api/logs/stream?sinceId="+logStreamEventID(ts), nil)
	options, err := parseLogStreamOptions(req)
	if err != nil {
		t.Fatalf("parseLogStreamOptions returned error: %v", err)
	}
	if !options.HasSinceTime {
		t.Fatal("expected sinceId to set replay timestamp")
	}
	if !options.SinceTime.Equal(ts) {
		t.Fatalf("sinceId timestamp = %v, want %v", options.SinceTime, ts)
	}
}

func TestParseLogStreamOptionsRejectsAmbiguousReplayCursor(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/logs/stream?sinceId=evt-1&sinceTime=2026-05-06T15:30:12%2B08:00", nil)
	if _, err := parseLogStreamOptions(req); err == nil {
		t.Fatal("expected error when sinceId and sinceTime are both provided")
	}
}

func TestLogStreamEventFromConnectionLog(t *testing.T) {
	entry := singbox.LogEntry{
		Time:    time.Date(2026, 5, 6, 15, 30, 12, 0, time.FixedZone("CST", 8*60*60)),
		Level:   "info",
		Message: "INFO outbound/trojan[台湾 05]: outbound connection to 104.18.32.47:443",
	}

	event, ok := logStreamEventFromEntry(entry, false)
	if !ok {
		t.Fatal("expected connection log to be streamed")
	}
	if event.Type != "connection" {
		t.Fatalf("type = %q, want connection", event.Type)
	}
	if event.Component != "outbound/trojan" || event.Tag != "台湾 05" {
		t.Fatalf("unexpected component/tag: %q %q", event.Component, event.Tag)
	}
	if event.Direction != "outbound" {
		t.Fatalf("direction = %q, want outbound", event.Direction)
	}
	if event.Action != "outbound connection to" || event.Endpoint != "104.18.32.47:443" {
		t.Fatalf("unexpected action/endpoint: %q %q", event.Action, event.Endpoint)
	}
}

func TestLogStreamEventFiltersNonConnectionByDefault(t *testing.T) {
	entry := singbox.LogEntry{
		Time:    time.Now(),
		Level:   "info",
		Message: "INFO dns: lookup succeed for example.com: 93.184.216.34",
	}

	if _, ok := logStreamEventFromEntry(entry, false); ok {
		t.Fatal("did not expect non-connection log in default stream")
	}
	event, ok := logStreamEventFromEntry(entry, true)
	if !ok {
		t.Fatal("expected include=all to stream non-connection logs")
	}
	if event.Type != "log" {
		t.Fatalf("type = %q, want log", event.Type)
	}
}

func TestLogStreamClientLimit(t *testing.T) {
	h := &Handlers{}
	releases := make([]func(), 0, maxLogStreamClients)
	for i := 0; i < maxLogStreamClients; i++ {
		release, ok := h.acquireLogStreamSlot()
		if !ok {
			t.Fatalf("slot %d was unexpectedly rejected", i)
		}
		releases = append(releases, release)
	}

	release, ok := h.acquireLogStreamSlot()
	if ok {
		release()
		t.Fatal("expected stream slot limit to reject extra client")
	}

	releases[0]()
	release, ok = h.acquireLogStreamSlot()
	if !ok {
		t.Fatal("expected a released stream slot to be reusable")
	}
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestWriteLogStreamEventUsesNDJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	ok := writeLogStreamEvent(rec, rec, logStreamEvent{
		Type: "heartbeat",
		Time: time.Date(2026, 5, 6, 15, 30, 12, 0, time.UTC),
	})
	if !ok {
		t.Fatal("writeLogStreamEvent returned false")
	}
	body := rec.Body.String()
	if body == "" || body[len(body)-1] != '\n' {
		t.Fatalf("expected trailing newline, got %q", body)
	}
	var event logStreamEvent
	if err := json.Unmarshal([]byte(body[:len(body)-1]), &event); err != nil {
		t.Fatalf("invalid json line: %v", err)
	}
	if event.Type != "heartbeat" {
		t.Fatalf("type = %q, want heartbeat", event.Type)
	}
}
