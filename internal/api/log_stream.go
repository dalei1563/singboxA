package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"singboxA/internal/singbox"
)

const (
	maxLogStreamClients          = 5
	logStreamHeartbeatInterval   = 20 * time.Second
	defaultLogStreamHistoryLimit = 1000
	maxLogStreamHistoryLimit     = 5000
)

type logStreamEvent struct {
	Type      string    `json:"type"`
	ID        string    `json:"id,omitempty"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message,omitempty"`
	Source    string    `json:"source,omitempty"`
	Component string    `json:"component,omitempty"`
	Tag       string    `json:"tag,omitempty"`
	Direction string    `json:"direction,omitempty"`
	Action    string    `json:"action,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
}

type logStreamOptions struct {
	IncludeAll   bool
	SinceTime    time.Time
	HasSinceTime bool
	HistoryLimit int
}

func (h *Handlers) StreamLogsNDJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	options, err := parseLogStreamOptions(r)
	if err != nil {
		h.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	release, ok := h.acquireLogStreamSlot()
	if !ok {
		h.sendError(w, http.StatusTooManyRequests, "too many log stream clients")
		return
	}
	defer release()

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	logChan := h.processMgr.SubscribeLogs()
	defer h.processMgr.UnsubscribeLogs(logChan)

	replayCutoff := time.Now()
	if options.HasSinceTime {
		if !h.replayLogStreamHistory(w, flusher, options, replayCutoff) {
			return
		}
	}

	heartbeat := time.NewTicker(logStreamHeartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case entry := <-logChan:
			event, ok := logStreamEventFromEntry(entry, options.IncludeAll)
			if !ok {
				continue
			}
			if !writeLogStreamEvent(w, flusher, event) {
				return
			}
		case <-heartbeat.C:
			if !writeLogStreamEvent(w, flusher, logStreamEvent{
				Type: "heartbeat",
				Time: time.Now(),
			}) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (h *Handlers) acquireLogStreamSlot() (func(), bool) {
	h.streamMu.Lock()
	if h.streamSlots == nil {
		h.streamSlots = make(chan struct{}, maxLogStreamClients)
	}
	slots := h.streamSlots
	h.streamMu.Unlock()

	select {
	case slots <- struct{}{}:
		return func() { <-slots }, true
	default:
		return func() {}, false
	}
}

func (h *Handlers) replayLogStreamHistory(w http.ResponseWriter, flusher http.Flusher, options logStreamOptions, cutoff time.Time) bool {
	logs, _, err := h.processMgr.GetPersistentLogs(options.HistoryLimit)
	if err != nil || len(logs) == 0 {
		logs = h.processMgr.GetLogs(options.HistoryLimit)
	}

	for _, entry := range logs {
		if !entry.Time.After(options.SinceTime) || entry.Time.After(cutoff) {
			continue
		}
		event, ok := logStreamEventFromEntry(entry, options.IncludeAll)
		if !ok {
			continue
		}
		if !writeLogStreamEvent(w, flusher, event) {
			return false
		}
	}
	return true
}

func parseLogStreamOptions(r *http.Request) (logStreamOptions, error) {
	options := logStreamOptions{
		IncludeAll:   strings.EqualFold(r.URL.Query().Get("include"), "all"),
		HistoryLimit: parseLogLimit(r, defaultLogStreamHistoryLimit, maxLogStreamHistoryLimit),
	}

	sinceID := strings.TrimSpace(r.URL.Query().Get("sinceId"))
	sinceTime := strings.TrimSpace(r.URL.Query().Get("sinceTime"))
	if sinceID != "" && sinceTime != "" {
		return options, fmt.Errorf("use either sinceId or sinceTime, not both")
	}

	if sinceID != "" {
		ts, err := parseLogStreamSinceID(sinceID)
		if err != nil {
			return options, err
		}
		options.SinceTime = ts
		options.HasSinceTime = true
		return options, nil
	}

	if sinceTime != "" {
		ts, err := parseLogStreamSinceTime(sinceTime)
		if err != nil {
			return options, err
		}
		options.SinceTime = ts
		options.HasSinceTime = true
	}

	return options, nil
}

func parseLogStreamSinceID(sinceID string) (time.Time, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(sinceID), "evt-")
	nanos, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanos <= 0 {
		return time.Time{}, fmt.Errorf("invalid sinceId")
	}
	return time.Unix(0, nanos), nil
}

func parseLogStreamSinceTime(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, nil
		}
		if ts, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid sinceTime")
}

func logStreamEventFromEntry(entry singbox.LogEntry, includeAll bool) (logStreamEvent, bool) {
	event := logStreamEvent{
		Type:    "log",
		ID:      logStreamEventID(entry.Time),
		Time:    entry.Time,
		Level:   entry.Level,
		Message: trimStreamMessage(entry.Message),
		Source:  "sing-box",
	}

	if fillConnectionFields(&event) {
		event.Type = "connection"
		return event, true
	}
	if includeAll {
		return event, true
	}
	return logStreamEvent{}, false
}

func fillConnectionFields(event *logStreamEvent) bool {
	lower := strings.ToLower(event.Message)
	if !strings.Contains(lower, "connection") {
		return false
	}

	prefix, body, ok := strings.Cut(event.Message, ": ")
	if !ok {
		return true
	}

	if idx := strings.IndexByte(prefix, ' '); idx >= 0 && idx+1 < len(prefix) {
		component := strings.TrimSpace(prefix[idx+1:])
		event.Component = component
		if start := strings.LastIndex(component, "["); start >= 0 && strings.HasSuffix(component, "]") {
			event.Tag = strings.TrimSuffix(component[start+1:], "]")
			event.Component = component[:start]
		}
		if strings.HasPrefix(event.Component, "inbound/") {
			event.Direction = "inbound"
		} else if strings.HasPrefix(event.Component, "outbound/") {
			event.Direction = "outbound"
		}
	}

	actions := []string{
		"outbound connection to ",
		"inbound connection from ",
		"inbound connection to ",
		"process connection from ",
		"process connection to ",
	}
	for _, actionPrefix := range actions {
		if strings.HasPrefix(body, actionPrefix) {
			event.Action = strings.TrimSpace(strings.TrimSuffix(actionPrefix, " "))
			event.Endpoint = firstConnectionEndpoint(strings.TrimSpace(strings.TrimPrefix(body, actionPrefix)))
			return true
		}
	}

	event.Action = firstConnectionEndpoint(body)
	return true
}

func firstConnectionEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if before, _, ok := strings.Cut(raw, ": "); ok {
		return strings.TrimSpace(before)
	}
	return raw
}

func logStreamEventID(ts time.Time) string {
	if ts.IsZero() {
		ts = time.Now()
	}
	return fmt.Sprintf("evt-%019d", ts.UnixNano())
}

func trimStreamMessage(message string) string {
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.TrimSpace(message)
	if len(message) <= 1024 {
		return message
	}
	return message[:1024]
}

func writeLogStreamEvent(w http.ResponseWriter, flusher http.Flusher, event logStreamEvent) bool {
	data, err := json.Marshal(event)
	if err != nil {
		return true
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
