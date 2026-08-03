// Package jobs is the gateway's single view of asynchronous work: anything
// longer than a request/response (stories, audiobooks, batches) registers
// here so one surface answers "what is running, how far along is it, and how
// do I stop it". Producers own their pipelines and report transitions;
// consumers list, poll, and cancel through the registry without knowing any
// pipeline's internals.
package jobs

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusComplete  Status = "complete"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// terminal reports whether a job can no longer change state.
func terminal(s Status) bool {
	return s == StatusComplete || s == StatusFailed || s == StatusCancelled
}

// Job is the consumer-facing snapshot of one unit of async work.
type Job struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Status    Status            `json:"status"`
	Progress  float64           `json:"progress"`
	Detail    string            `json:"detail,omitempty"`
	Error     string            `json:"error,omitempty"`
	Result    map[string]string `json:"result,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// maxFinishedJobs bounds how many terminal jobs the registry retains; the
// oldest finished jobs are evicted first. Active jobs are never evicted.
const maxFinishedJobs = 100

type tracked struct {
	job    Job
	cancel func() error
}

// Registry is a concurrency-safe, in-memory job table. Artifacts persist in
// their pipelines' stores (stories, library); the registry only holds the
// coordination state, so a restart forgets finished jobs but loses no output.
type Registry struct {
	mu   sync.Mutex
	jobs map[string]*tracked
	now  func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		jobs: make(map[string]*tracked),
		now:  func() time.Time { return time.Now().UTC() },
	}
}

// Track registers a new job under the producer's id. cancel, when non-nil, is
// invoked by Cancel to ask the pipeline to stop; the pipeline confirms by
// calling MarkCancelled once it has actually wound down.
func (r *Registry) Track(id, kind string, cancel func()) {
	var cancellable func() error
	if cancel != nil {
		cancellable = func() error {
			cancel()
			return nil
		}
	}
	r.TrackCancellable(id, kind, cancellable)
}

// TrackCancellable registers a job whose producer can refuse cancellation
// once it reaches an atomic commit/promotion phase.
func (r *Registry) TrackCancellable(id, kind string, cancel func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.jobs[id] = &tracked{
		job: Job{
			ID:        id,
			Kind:      kind,
			Status:    StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		cancel: cancel,
	}
	r.evictLocked()
}

// Update reports pipeline progress. Unknown ids and terminal jobs are
// ignored so late goroutine updates cannot resurrect a finished job.
func (r *Registry) Update(id string, progress float64, detail string) {
	r.mutate(id, func(j *Job) {
		j.Status = StatusRunning
		j.Progress = progress
		j.Detail = detail
	})
}

func (r *Registry) Complete(id string, result map[string]string) {
	r.mutate(id, func(j *Job) {
		j.Status = StatusComplete
		j.Progress = 1
		j.Detail = ""
		j.Result = result
	})
}

func (r *Registry) Fail(id string, message string) {
	r.mutate(id, func(j *Job) {
		j.Status = StatusFailed
		j.Error = message
	})
}

func (r *Registry) MarkCancelled(id string) {
	r.mutate(id, func(j *Job) {
		j.Status = StatusCancelled
	})
}

func (r *Registry) mutate(id string, apply func(*Job)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.jobs[id]
	if !ok || terminal(t.job.Status) {
		return
	}
	apply(&t.job)
	t.job.UpdatedAt = r.now()
}

// Cancel asks a job's pipeline to stop. The cancel delegate runs outside the
// registry lock because pipelines typically call back into MarkCancelled.
func (r *Registry) Cancel(id string) (Job, error) {
	r.mu.Lock()
	t, ok := r.jobs[id]
	if !ok {
		r.mu.Unlock()
		return Job{}, fmt.Errorf("unknown job %q", id)
	}
	if terminal(t.job.Status) {
		job := t.job
		r.mu.Unlock()
		return job, fmt.Errorf("job %q is already %s", id, job.Status)
	}
	cancel := t.cancel
	r.mu.Unlock()

	if cancel != nil {
		if err := cancel(); err != nil {
			job, _ := r.Get(id)
			return job, err
		}
	}
	job, _ := r.Get(id)
	return job, nil
}

func (r *Registry) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.jobs[id]
	if !ok {
		return Job{}, false
	}
	return t.job, true
}

// List returns every tracked job, newest first.
func (r *Registry) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.jobs))
	for _, t := range r.jobs {
		out = append(out, t.job)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// evictLocked drops the oldest finished jobs beyond the retention cap.
func (r *Registry) evictLocked() {
	var finished []*tracked
	for _, t := range r.jobs {
		if terminal(t.job.Status) {
			finished = append(finished, t)
		}
	}
	if len(finished) <= maxFinishedJobs {
		return
	}
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].job.UpdatedAt.Before(finished[j].job.UpdatedAt)
	})
	for _, t := range finished[:len(finished)-maxFinishedJobs] {
		delete(r.jobs, t.job.ID)
	}
}
