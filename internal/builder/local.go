package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/openforge/openforge/internal/asu"
	"github.com/openforge/openforge/internal/config"
)

// LocalBackend implements asu.Backend using the built-in build queue. This is
// OpenForge's own implementation of the ASU build service.
type LocalBackend struct {
	cfg    *config.Config
	store  *Store
	queue  *Queue
	events *EventLog
}

// NewLocalBackend wires up the container engine, artifact store, event log and
// worker pool.
func NewLocalBackend(cfg *config.Config, paths PathResolver, logger *slog.Logger) (*LocalBackend, error) {
	store, err := NewStore(cfg.PublicPath)
	if err != nil {
		return nil, err
	}
	events := NewEventLog(eventsPath(cfg.PublicPath))

	engine, err := DetectEngine(cfg.ContainerEngine, cfg.ContainerHost)
	if err != nil {
		logger.Warn("no container engine available; builds will fail until one is installed", "err", err)
		engine = unavailableEngine{err: err}
	} else if !engine.Available(context.Background()) {
		logger.Warn("container engine is not responding", "engine", engine.Name())
	} else {
		host := ""
		if cli, ok := engine.(*cliEngine); ok {
			host = cli.Host()
		}
		logger.Info("using container engine", "engine", engine.Name(), "host", host)
	}

	pipeline := NewPipeline(cfg, engine, store, paths, logger)
	queue := NewQueue(cfg, pipeline, store, events, logger)
	return &LocalBackend{cfg: cfg, store: store, queue: queue, events: events}, nil
}

// Endpoint implements asu.Backend.
func (b *LocalBackend) Endpoint() string { return "builtin" }

// Build implements asu.Backend.
func (b *LocalBackend) Build(_ context.Context, req *asu.BuildRequest) (*asu.BackendResponse, error) {
	if max := b.cfg.MaxPendingJobs; max > 0 && b.queue.Len() >= max {
		return errorResponse(529, "Server overloaded",
			fmt.Sprintf("server overload, queue contains too many build requests: %d", b.queue.Len())), nil
	}
	job, created := b.queue.Enqueue(req)
	if !created && job.State() == StateDone {
		b.events.RecordCacheHit()
	}
	return b.jobResponse(job)
}

// BuildStatus implements asu.Backend.
func (b *LocalBackend) BuildStatus(_ context.Context, hash string) (*asu.BackendResponse, error) {
	job := b.queue.Get(hash)
	if job == nil {
		return errorResponse(http.StatusNotFound, "Not Found",
			"could not find provided request hash"), nil
	}
	return b.jobResponse(job)
}

// Store implements asu.Backend by serving local artifacts.
func (b *LocalBackend) Store(_ context.Context, path string) (*http.Response, error) {
	file, size, name, err := b.store.Open(path)
	if err != nil {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader("not found\n")),
		}, nil
	}
	header := http.Header{}
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Length", strconv.FormatInt(size, 10))
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	header.Set("X-Content-Type-Options", "nosniff")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: file}, nil
}

// Stats implements asu.Backend.
func (b *LocalBackend) Stats(_ context.Context) (*asu.BackendResponse, error) {
	return jsonResponse(200, map[string]any{"queue_length": b.queue.Len()}), nil
}

// StatsSummary implements asu.StatsProvider.
func (b *LocalBackend) StatsSummary() map[string]any {
	return map[string]any{
		"queue_length": b.queue.Len(),
		"builds_24h":   b.events.Successes24h(),
	}
}

// BuildsPerDay implements asu.StatsProvider.
func (b *LocalBackend) BuildsPerDay() map[string]any { return b.events.BuildsPerDay() }

// BuildsByVersion implements asu.StatsProvider.
func (b *LocalBackend) BuildsByVersion() map[string]any { return b.events.BuildsByVersion() }

// TopPackages implements asu.StatsProvider.
func (b *LocalBackend) TopPackages() map[string]any { return b.events.TopPackages() }

// BuildErrors implements asu.StatsProvider.
func (b *LocalBackend) BuildErrors() string { return b.events.BuildErrors() }

// Close shuts down the queue and flushes events.
func (b *LocalBackend) Close() {
	if b.queue != nil {
		b.queue.Close()
	}
}

func (b *LocalBackend) jobResponse(job *Job) (*asu.BackendResponse, error) {
	payload, status := job.Response()
	imagebuilderStatus, position := job.HeaderStatus()
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-Imagebuilder-Status", imagebuilderStatus)
	header.Set("X-Queue-Position", strconv.Itoa(position))
	return &asu.BackendResponse{StatusCode: status, Header: header, Body: body}, nil
}

func errorResponse(status int, title, detail string) *asu.BackendResponse {
	body, _ := json.Marshal(map[string]any{"status": status, "title": title, "detail": detail})
	return &asu.BackendResponse{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}
}

func jsonResponse(status int, payload any) *asu.BackendResponse {
	body, _ := json.Marshal(payload)
	return &asu.BackendResponse{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}
}

// unavailableEngine is used when no container runtime is installed.
type unavailableEngine struct{ err error }

func (u unavailableEngine) Name() string                       { return "none" }
func (u unavailableEngine) Available(context.Context) bool     { return false }
func (u unavailableEngine) Pull(context.Context, string) error { return u.err }
func (u unavailableEngine) Create(context.Context, ContainerSpec) (string, error) {
	return "", u.err
}
func (u unavailableEngine) Start(context.Context, string) error { return u.err }
func (u unavailableEngine) Exec(context.Context, string, ExecSpec) (ExecResult, error) {
	return ExecResult{}, u.err
}
func (u unavailableEngine) CopyIn(context.Context, string, string, map[string][]byte) error {
	return u.err
}
func (u unavailableEngine) CopyOut(context.Context, string, string, string) error { return u.err }
func (u unavailableEngine) Remove(context.Context, string) error                  { return nil }
