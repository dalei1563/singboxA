package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLogLimit(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want int
	}{
		{name: "default", url: "/api/logs/history", want: 100},
		{name: "valid", url: "/api/logs/history?limit=250", want: 250},
		{name: "invalid", url: "/api/logs/history?limit=oops", want: 100},
		{name: "negative", url: "/api/logs/history?limit=-1", want: 100},
		{name: "capped", url: "/api/logs/history?limit=9999", want: 500},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			if got := parseLogLimit(req, 100, 500); got != tc.want {
				t.Fatalf("parseLogLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseJournalLogEntry(t *testing.T) {
	entry, ok := parseJournalLogEntry(`{"__REALTIME_TIMESTAMP":"1777620000123456","PRIORITY":"3","MESSAGE":"sing-box exited: exit status 1","_PID":"123","_SYSTEMD_UNIT":"singboxA.service"}`)
	if !ok {
		t.Fatal("expected journal entry to parse")
	}
	if entry.Level != "error" {
		t.Fatalf("level = %q, want error", entry.Level)
	}
	if entry.Message != "sing-box exited: exit status 1" {
		t.Fatalf("message = %q", entry.Message)
	}
	if entry.PID != "123" || entry.Unit != "singboxA.service" {
		t.Fatalf("unexpected source fields: pid=%q unit=%q", entry.PID, entry.Unit)
	}
	want := time.Unix(0, 1777620000123456*int64(time.Microsecond))
	if !entry.Time.Equal(want) {
		t.Fatalf("time = %v, want %v", entry.Time, want)
	}
}
