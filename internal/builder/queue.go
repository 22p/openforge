package builder

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/openforge/openforge/internal/asu"
	"github.com/openforge/openforge/internal/config"
)

// Queue is an in-process build queue with a fixed worker pool. Finished jobs
// are cached in memory and on disk for the configured TTLs.
type Queue struct {
	cfg      *config.Config
	pipeline *Pipeline
	store    *Store
	events   *EventLog
	logger   *slog.Logger

	mu     sync.Mutex
	cond   *sync.Cond
	jobs   map[string]*Job
	order  []string
	closed bool
}

// NewQueue starts the worker pool and returns the queue.
func NewQueue(cfg *config.Config, pipeline *Pipeline, store *Store, events *EventLog, logger *slog.Logger) *Queue {
	q := &Queue{
		cfg:      cfg,
		pipeline: pipeline,
		store:    store,
		events:   events,
		logger:   logger,
		jobs:     map[string]*Job{},
	}
	q.cond = sync.NewCond(&q.mu)
	for i := 0; i < cfg.Workers; i++ {
		go q.worker(i)
	}
	go q.janitor()
	return q
}

// Len returns the number of pending (not yet started) jobs.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}

// Enqueue adds a job if it does not already exist, returning the job and
// whether it was newly created.
func (q *Queue) Enqueue(req *asu.BuildRequest) (*Job, bool) {
	id := req.RequestHash()
	q.mu.Lock()
	if existing, ok := q.jobs[id]; ok {
		q.mu.Unlock()
		return existing, false
	}
	job := newJob(id, req)
	job.queuePosition = len(q.order)
	q.jobs[id] = job
	q.order = append(q.order, id)
	q.reindexLocked()
	q.mu.Unlock()

	q.events.RecordRequest()
	q.cond.Signal()
	return job, true
}

// Get returns a job by request hash, consulting the disk cache on a miss.
func (q *Queue) Get(hash string) *Job {
	q.mu.Lock()
	if job, ok := q.jobs[hash]; ok {
		q.mu.Unlock()
		if q.expired(job) {
			return nil
		}
		return job
	}
	q.mu.Unlock()

	job := q.store.LoadJob(hash)
	if job == nil || q.expired(job) {
		return nil
	}
	q.mu.Lock()
	if existing, ok := q.jobs[hash]; ok {
		q.mu.Unlock()
		return existing
	}
	q.jobs[hash] = job
	q.mu.Unlock()
	return job
}

func (q *Queue) reindexLocked() {
	for i, id := range q.order {
		if job, ok := q.jobs[id]; ok {
			job.mu.Lock()
			job.queuePosition = i
			job.mu.Unlock()
		}
	}
}

func (q *Queue) worker(id int) {
	for {
		q.mu.Lock()
		for len(q.order) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		hash := q.order[0]
		q.order = q.order[1:]
		q.reindexLocked()
		job := q.jobs[hash]
		q.mu.Unlock()

		if job == nil {
			continue
		}
		job.markRunning()
		q.events.RecordCacheMiss()
		q.logger.Info("build started", "hash", job.ID, "worker", id)

		result, err := q.pipeline.Build(context.Background(), job)
		if err != nil {
			detail := err.Error()
			var failure *buildFailure
			if errors.As(err, &failure) {
				detail = failure.detail
			}
			job.markFailed(detail, job.Stderr())
			q.events.RecordFailure(job, detail)
			q.logger.Warn("build failed", "hash", job.ID, "detail", detail)
		} else {
			job.markDone(result)
			q.events.RecordSuccess(job)
			q.logger.Info("build finished", "hash", job.ID)
		}
		if err := q.store.SaveJob(job); err != nil {
			q.logger.Warn("failed to persist job", "hash", job.ID, "err", err)
		}
	}
}

func (q *Queue) janitor() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		q.expire()
	}
}

func (q *Queue) expire() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, job := range q.jobs {
		if q.expiredLocked(job) {
			delete(q.jobs, id)
			q.store.RemoveJob(id)
		}
	}
}

func (q *Queue) expired(job *Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.expiredLocked(job)
}

func (q *Queue) expiredLocked(job *Job) bool {
	if job == nil {
		return true
	}
	finished := job.FinishedAt()
	if finished.IsZero() {
		return false // queued or running
	}
	var ttl time.Duration
	switch job.State() {
	case StateDone:
		req := job.Request()
		switch {
		case req != nil && req.Defaults != nil && *req.Defaults != "":
			ttl = q.cfg.BuildDefaultsTTLDuration()
		case req != nil && len(req.PackagesVersions) > 0:
			ttl = q.cfg.BuildTTLDuration()
		default:
			ttl = q.cfg.BuildTTLUnversionedDuration()
		}
	case StateFailed:
		ttl = q.cfg.BuildFailureTTLDuration()
	default:
		return false
	}
	return time.Since(finished) > ttl
}

// Close stops accepting work and flushes the event log.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
	q.events.Close()
}
