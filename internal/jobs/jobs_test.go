package jobs

import (
	"fmt"
	"testing"
)

func TestLifecycleAndOrdering(t *testing.T) {
	r := NewRegistry()
	r.Track("a", "story", nil)
	r.Track("b", "audiobook", nil)

	r.Update("a", 0.5, "synthesizing")
	if j, _ := r.Get("a"); j.Status != StatusRunning || j.Progress != 0.5 || j.Detail != "synthesizing" {
		t.Fatalf("update not applied: %+v", j)
	}

	r.Complete("a", map[string]string{"artifactUrl": "/x"})
	j, _ := r.Get("a")
	if j.Status != StatusComplete || j.Progress != 1 || j.Result["artifactUrl"] != "/x" {
		t.Fatalf("complete not applied: %+v", j)
	}

	// Terminal jobs ignore late updates.
	r.Update("a", 0.2, "zombie goroutine")
	if j, _ := r.Get("a"); j.Status != StatusComplete {
		t.Fatalf("terminal job mutated: %+v", j)
	}

	r.Fail("b", "boom")
	if j, _ := r.Get("b"); j.Status != StatusFailed || j.Error != "boom" {
		t.Fatalf("fail not applied: %+v", j)
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(list))
	}
}

func TestCancelInvokesDelegateOutsideLock(t *testing.T) {
	r := NewRegistry()
	// The delegate calls back into the registry, as the story manager does;
	// this deadlocks if Cancel holds the lock across the delegate.
	r.Track("j", "story", func() { r.MarkCancelled("j") })

	job, err := r.Cancel("j")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if job.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %+v", job)
	}
	if _, err := r.Cancel("j"); err == nil {
		t.Fatal("expected error cancelling a terminal job")
	}
	if _, err := r.Cancel("missing"); err == nil {
		t.Fatal("expected error cancelling an unknown job")
	}
}

func TestFinishedJobEviction(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < maxFinishedJobs+10; i++ {
		id := fmt.Sprintf("j%03d", i)
		r.Track(id, "test", nil)
		r.Complete(id, nil)
	}
	// One active job must survive any amount of finished churn.
	r.Track("active", "test", nil)
	r.Track("trigger", "test", nil)
	r.Complete("trigger", nil)
	r.Track("post", "test", nil)

	if _, ok := r.Get("active"); !ok {
		t.Fatal("active job was evicted")
	}
	finished := 0
	for _, j := range r.List() {
		if terminal(j.Status) {
			finished++
		}
	}
	if finished > maxFinishedJobs {
		t.Fatalf("finished jobs not capped: %d", finished)
	}
}
