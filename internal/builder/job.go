package builder

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/openforge/openforge/internal/asu"
)

// State is the lifecycle state of a build job.
type State string

// Job states, mirroring RQ's queued/started/finished/failed.
const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// Job is a single build request tracked by the queue.
type Job struct {
	ID string `json:"request_hash"`

	mu                 sync.Mutex
	state              State
	detail             string
	imagebuilderStatus string
	queuePosition      int
	enqueuedAt         time.Time
	startedAt          time.Time
	finishedAt         time.Time
	stdout             string
	stderr             string
	result             map[string]any
	request            *asu.BuildRequest
}

func newJob(id string, req *asu.BuildRequest) *Job {
	return &Job{
		ID:                 id,
		state:              StateQueued,
		detail:             "queued",
		imagebuilderStatus: "queued",
		enqueuedAt:         time.Now().UTC(),
		request:            req,
	}
}

// State returns the current job state.
func (j *Job) State() State {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// Request returns the build request.
func (j *Job) Request() *asu.BuildRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.request
}

// FinishedAt returns the completion time (zero while unfinished).
func (j *Job) FinishedAt() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.finishedAt
}

// Stderr returns the captured stderr output.
func (j *Job) Stderr() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stderr
}

func (j *Job) setImagebuilderStatus(status string) {
	j.mu.Lock()
	j.imagebuilderStatus = status
	j.mu.Unlock()
}

func (j *Job) setOutput(stdout, stderr string) {
	j.mu.Lock()
	j.stdout = stdout
	j.stderr = stderr
	j.mu.Unlock()
}

func (j *Job) markRunning() {
	j.mu.Lock()
	j.state = StateRunning
	j.detail = "started"
	j.startedAt = time.Now().UTC()
	j.mu.Unlock()
}

func (j *Job) markDone(result map[string]any) {
	j.mu.Lock()
	j.state = StateDone
	j.detail = "done"
	j.imagebuilderStatus = "done"
	j.result = result
	j.finishedAt = time.Now().UTC()
	j.mu.Unlock()
}

func (j *Job) markFailed(detail, stderr string) {
	j.mu.Lock()
	j.state = StateFailed
	if detail == "" || detail == "init" {
		detail = "failed"
	}
	j.detail = detail
	if stderr != "" {
		j.stderr = stderr
	}
	j.imagebuilderStatus = "failed"
	j.finishedAt = time.Now().UTC()
	j.mu.Unlock()
}

// Response renders the ASU compatible job payload and HTTP status code.
func (j *Job) Response() (map[string]any, int) {
	j.mu.Lock()
	defer j.mu.Unlock()

	out := map[string]any{}
	for k, v := range j.result {
		out[k] = v
	}
	out["request"] = j.request
	out["request_hash"] = j.ID
	if !j.enqueuedAt.IsZero() {
		out["enqueued_at"] = j.enqueuedAt.Format(time.RFC3339Nano)
	}

	status := 202
	switch j.state {
	case StateQueued:
		out["status"] = 202
		out["detail"] = "queued"
		out["imagebuilder_status"] = "queued"
		out["queue_position"] = j.queuePosition
	case StateRunning:
		out["status"] = 202
		out["detail"] = "started"
		ib := j.imagebuilderStatus
		if ib == "" {
			ib = "init"
		}
		out["imagebuilder_status"] = ib
	case StateDone:
		out["status"] = 200
		out["detail"] = "done"
		out["imagebuilder_status"] = "done"
		status = 200
	case StateFailed:
		out["status"] = 500
		out["detail"] = j.detail
		out["imagebuilder_status"] = "failed"
		if j.stderr != "" {
			out["stderr"] = j.stderr
		}
		status = 500
	}
	return out, status
}

// HeaderStatus returns the X-Imagebuilder-Status value and queue position.
func (j *Job) HeaderStatus() (string, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	status := j.imagebuilderStatus
	if status == "" {
		status = "init"
	}
	return status, j.queuePosition
}

// persisted is the on-disk representation of a job.
type persisted struct {
	ID                 string            `json:"request_hash"`
	State              State             `json:"state"`
	Detail             string            `json:"detail"`
	ImagebuilderStatus string            `json:"imagebuilder_status"`
	EnqueuedAt         time.Time         `json:"enqueued_at"`
	StartedAt          time.Time         `json:"started_at"`
	FinishedAt         time.Time         `json:"finished_at"`
	Stdout             string            `json:"stdout,omitempty"`
	Stderr             string            `json:"stderr,omitempty"`
	Result             map[string]any    `json:"result,omitempty"`
	Request            *asu.BuildRequest `json:"request"`
}

func (j *Job) toPersisted() persisted {
	j.mu.Lock()
	defer j.mu.Unlock()
	return persisted{
		ID:                 j.ID,
		State:              j.state,
		Detail:             j.detail,
		ImagebuilderStatus: j.imagebuilderStatus,
		EnqueuedAt:         j.enqueuedAt,
		StartedAt:          j.startedAt,
		FinishedAt:         j.finishedAt,
		Stdout:             j.stdout,
		Stderr:             j.stderr,
		Result:             j.result,
		Request:            j.request,
	}
}

func jobFromPersisted(p persisted) *Job {
	return &Job{
		ID:                 p.ID,
		state:              p.State,
		detail:             p.Detail,
		imagebuilderStatus: p.ImagebuilderStatus,
		enqueuedAt:         p.EnqueuedAt,
		startedAt:          p.StartedAt,
		finishedAt:         p.FinishedAt,
		stdout:             p.Stdout,
		stderr:             p.Stderr,
		result:             p.Result,
		request:            p.Request,
	}
}

func (j *Job) marshalRecord() ([]byte, error) {
	return json.MarshalIndent(j.toPersisted(), "", "  ")
}
