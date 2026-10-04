package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxEvents    = 100000
	maxErrors    = 5000
	eventsPerDay = 30
)

type eventRecord struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Version  string    `json:"version,omitempty"`
	Target   string    `json:"target,omitempty"`
	Profile  string    `json:"profile,omitempty"`
	Packages []string  `json:"packages,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// EventLog records build events and derives the statistics served by the
// /api/v1/stats/* endpoints. It is a self-contained alternative to ASU's
// Redis TimeSeries dependency.
type EventLog struct {
	mu     sync.Mutex
	path   string
	events []eventRecord
	dirty  bool
	stop   chan struct{}
	once   sync.Once
}

// NewEventLog loads (or creates) the event log and starts background flushing.
func NewEventLog(path string) *EventLog {
	log := &EventLog{path: path, stop: make(chan struct{})}
	log.load()
	go log.flusher()
	return log
}

func (e *EventLog) load() {
	data, err := os.ReadFile(e.path)
	if err != nil {
		return
	}
	var events []eventRecord
	if json.Unmarshal(data, &events) == nil {
		e.events = events
	}
}

func (e *EventLog) flusher() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			e.flush()
			return
		case <-ticker.C:
			e.flush()
		}
	}
}

// Close stops background flushing and persists the log synchronously.
func (e *EventLog) Close() {
	e.once.Do(func() {
		close(e.stop)
		// The flusher goroutine may not run before the process exits, so make
		// sure the final state is written here as well.
		e.flush()
	})
}

func (e *EventLog) flush() {
	e.mu.Lock()
	if !e.dirty {
		e.mu.Unlock()
		return
	}
	data, err := json.Marshal(e.events)
	e.dirty = false
	e.mu.Unlock()
	if err != nil {
		return
	}
	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, e.path)
}

func (e *EventLog) record(ev eventRecord) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	e.mu.Lock()
	e.events = append(e.events, ev)
	if len(e.events) > maxEvents {
		e.events = e.events[len(e.events)-maxEvents:]
	}
	e.dirty = true
	e.mu.Unlock()
}

// RecordRequest records an incoming build request.
func (e *EventLog) RecordRequest() { e.record(eventRecord{Kind: "request"}) }

// RecordCacheHit records that a request was served from cache.
func (e *EventLog) RecordCacheHit() { e.record(eventRecord{Kind: "cache-hit"}) }

// RecordCacheMiss records that a request was sent to the builder.
func (e *EventLog) RecordCacheMiss() { e.record(eventRecord{Kind: "cache-miss"}) }

// RecordSuccess records a successful build.
func (e *EventLog) RecordSuccess(job *Job) {
	req := job.Request()
	e.record(eventRecord{
		Kind:     "success",
		Version:  derefStr(req.Version),
		Target:   derefStr(req.Target),
		Profile:  derefStr(req.Profile),
		Packages: append([]string(nil), req.Packages...),
	})
}

// RecordFailure records a failed build.
func (e *EventLog) RecordFailure(job *Job, message string) {
	req := job.Request()
	clean := message
	clean = strings.Join(strings.Fields(clean), " ")
	if len(clean) > 200 {
		clean = clean[:200]
	}
	e.record(eventRecord{
		Kind:    "failure",
		Version: derefStr(req.Version),
		Target:  derefStr(req.Target),
		Profile: derefStr(req.Profile),
		Message: fmt.Sprintf("%s:%s:%s %s", derefStr(req.Version), derefStr(req.Target), derefStr(req.Profile), clean),
	})
}

func (e *EventLog) snapshot() []eventRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]eventRecord(nil), e.events...)
}

// Successes24h counts successful builds in the last day.
func (e *EventLog) Successes24h() int {
	since := time.Now().UTC().Add(-24 * time.Hour)
	count := 0
	for _, ev := range e.snapshot() {
		if ev.Kind == "success" && ev.At.After(since) {
			count++
		}
	}
	return count
}

// BuildsPerDay returns the ASU /builds-per-day structure for the last 30 days.
func (e *EventLog) BuildsPerDay() map[string]any {
	stop := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	start := stop.Add(-eventsPerDay * 24 * time.Hour)

	labels := make([]string, eventsPerDay)
	for i := 0; i < eventsPerDay; i++ {
		labels[i] = start.Add(time.Duration(i)*24*time.Hour).Format("2006-01-02") + "Z"
	}
	requests := make([]int, eventsPerDay)
	cacheHits := make([]int, eventsPerDay)
	failures := make([]int, eventsPerDay)

	for _, ev := range e.snapshot() {
		if ev.At.Before(start) || !ev.At.Before(stop) {
			continue
		}
		idx := int(ev.At.Sub(start) / (24 * time.Hour))
		if idx < 0 || idx >= eventsPerDay {
			continue
		}
		switch ev.Kind {
		case "request":
			requests[idx]++
		case "cache-hit":
			cacheHits[idx]++
		case "failure":
			failures[idx]++
		}
	}
	return map[string]any{
		"labels": labels,
		"datasets": []map[string]any{
			{"label": "Requests", "data": requests, "color": "green"},
			{"label": "Cache-Hits", "data": cacheHits, "color": "orange"},
			{"label": "Failures", "data": failures, "color": "red"},
		},
	}
}

// BuildsByVersion returns per-version build totals for the last half year.
func (e *EventLog) BuildsByVersion() map[string]any {
	stop := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	start := stop.Add(-26 * 7 * 24 * time.Hour)

	totals := map[string]int{}
	for _, ev := range e.snapshot() {
		if ev.Kind != "success" || ev.At.Before(start) {
			continue
		}
		version := ev.Version
		if strings.Contains(version, ".") && len(version) >= 5 {
			version = version[:5]
		}
		totals[version]++
	}

	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)

	datasets := make([]map[string]any, 0, len(names))
	for _, name := range names {
		datasets = append(datasets, map[string]any{
			"label": name,
			"data":  []int{totals[name]},
		})
	}
	return map[string]any{
		"labels":   []string{stop.Format("2006-01-02") + "Z"},
		"datasets": datasets,
	}
}

// TopPackages returns the most requested packages over the last 30 days.
func (e *EventLog) TopPackages() map[string]any {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	counts := map[string]int{}
	for _, ev := range e.snapshot() {
		if ev.Kind != "success" || ev.At.Before(since) {
			continue
		}
		for _, pkg := range ev.Packages {
			counts[pkg]++
		}
	}
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for name, count := range counts {
		pairs = append(pairs, pair{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > 100 {
		pairs = pairs[:100]
	}
	out := make([]map[string]any, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, map[string]any{"name": p.name, "count": p.count})
	}
	return map[string]any{"packages": out, "branch": nil, "days": 30}
}

// BuildErrors returns a plain-text summary of recent failures.
func (e *EventLog) BuildErrors() string {
	var messages []string
	for _, ev := range e.snapshot() {
		if ev.Kind == "failure" && ev.Message != "" {
			messages = append(messages, ev.Message)
		}
	}
	if len(messages) == 0 {
		return "No build errors recorded."
	}
	if len(messages) > maxErrors {
		messages = messages[len(messages)-maxErrors:]
	}
	// Newest first.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return strings.Join(messages, "\n")
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// eventsPath is a helper used by tests and callers to locate the event log.
func eventsPath(publicPath string) string {
	return filepath.Join(publicPath, "events.json")
}
