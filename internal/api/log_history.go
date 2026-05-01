package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"singboxA/internal/singbox"
)

const (
	defaultLogHistoryLimit = 500
	maxLogHistoryLimit     = 5000
	journalTimeout         = 5 * time.Second
)

type logHistoryResponse struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Limit       int                   `json:"limit"`
	Memory      logEntrySource        `json:"memory"`
	Persistent  logEntrySource        `json:"persistent"`
	Journal     *journalHistorySource `json:"journal,omitempty"`
}

type logEntrySource struct {
	Count   int                `json:"count"`
	Path    string             `json:"path,omitempty"`
	Error   string             `json:"error,omitempty"`
	Entries []singbox.LogEntry `json:"entries"`
}

type journalHistorySource struct {
	Count   int               `json:"count"`
	Service string            `json:"service"`
	Error   string            `json:"error,omitempty"`
	Entries []journalLogEntry `json:"entries"`
}

type journalLogEntry struct {
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
	PID      string    `json:"pid,omitempty"`
	Unit     string    `json:"unit,omitempty"`
	Priority string    `json:"priority,omitempty"`
}

type rawJournalEntry struct {
	RealtimeTimestamp string `json:"__REALTIME_TIMESTAMP"`
	Priority          string `json:"PRIORITY"`
	Message           string `json:"MESSAGE"`
	PID               string `json:"_PID"`
	Unit              string `json:"_SYSTEMD_UNIT"`
}

func (h *Handlers) GetLogHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limit := parseLogLimit(r, defaultLogHistoryLimit, maxLogHistoryLimit)
	includeJournal := r.URL.Query().Get("journal") != "false"
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	if len(since) > 128 {
		h.sendError(w, http.StatusBadRequest, "since is too long")
		return
	}

	memoryLogs := h.processMgr.GetLogs(limit)
	persistentLogs, path, persistentErr := h.processMgr.GetPersistentLogs(limit)
	response := logHistoryResponse{
		GeneratedAt: time.Now(),
		Limit:       limit,
		Memory: logEntrySource{
			Count:   len(memoryLogs),
			Entries: memoryLogs,
		},
		Persistent: logEntrySource{
			Count:   len(persistentLogs),
			Path:    path,
			Entries: persistentLogs,
		},
	}
	if persistentErr != nil {
		response.Persistent.Error = persistentErr.Error()
	}

	if includeJournal {
		journal := &journalHistorySource{Service: "singboxA"}
		entries, err := readSingBoxAServiceJournal(limit, since)
		if err != nil {
			journal.Error = err.Error()
		} else {
			journal.Entries = entries
			journal.Count = len(entries)
		}
		response.Journal = journal
	}

	h.sendJSON(w, response)
}

func parseLogLimit(r *http.Request, defaultLimit, maxLimit int) int {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit
}

func readSingBoxAServiceJournal(limit int, since string) ([]journalLogEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), journalTimeout)
	defer cancel()

	args := []string{"-u", "singboxA", "--no-pager", "-o", "json", "-n", strconv.Itoa(limit)}
	if since != "" {
		args = append(args, "--since", since)
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("journalctl failed: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("journalctl failed: %w", err)
	}

	entries := make([]journalLogEntry, 0)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, ok := parseJournalLogEntry(line)
		if ok {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func parseJournalLogEntry(line string) (journalLogEntry, bool) {
	var raw rawJournalEntry
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return journalLogEntry{}, false
	}
	if raw.Message == "" {
		return journalLogEntry{}, false
	}

	return journalLogEntry{
		Time:     journalTimestamp(raw.RealtimeTimestamp),
		Level:    journalPriorityToLevel(raw.Priority),
		Message:  raw.Message,
		PID:      raw.PID,
		Unit:     raw.Unit,
		Priority: raw.Priority,
	}, true
}

func journalTimestamp(raw string) time.Time {
	micros, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || micros <= 0 {
		return time.Time{}
	}
	return time.Unix(0, micros*int64(time.Microsecond))
}

func journalPriorityToLevel(priority string) string {
	switch priority {
	case "0", "1", "2":
		return "fatal"
	case "3":
		return "error"
	case "4":
		return "warn"
	case "7":
		return "debug"
	default:
		return "info"
	}
}
