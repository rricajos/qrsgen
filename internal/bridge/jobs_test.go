package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJobStore_CreateAndGet(t *testing.T) {
	s := NewJobStore()
	job := s.Create("test", "inst1")
	if job.ID == "" || job.Status != JobPending {
		t.Errorf("got %+v", job)
	}
	got, ok := s.Get(job.ID)
	if !ok || got.ID != job.ID {
		t.Error("not retrievable")
	}
}

func TestJobStore_StartCompleteFail(t *testing.T) {
	s := NewJobStore()
	job := s.Create("test", "inst1")

	s.Start(job.ID)
	got, _ := s.Get(job.ID)
	if got.Status != JobRunning {
		t.Errorf("status = %q", got.Status)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}

	s.Complete(job.ID, map[string]any{"x": 1})
	got, _ = s.Get(job.ID)
	if got.Status != JobCompleted {
		t.Errorf("status = %q", got.Status)
	}
	if got.Result == nil {
		t.Error("Result should be set")
	}
}

func TestJobStore_FailWithError(t *testing.T) {
	s := NewJobStore()
	job := s.Create("test", "inst1")
	s.Fail(job.ID, errors.New("boom"))
	got, _ := s.Get(job.ID)
	if got.Status != JobFailed || got.Error != "boom" {
		t.Errorf("got %+v", got)
	}
}

func TestJobStore_RunAsyncCompletes(t *testing.T) {
	s := NewJobStore()
	job := s.Create("test", "inst1")
	s.RunAsync(job, func(_ context.Context) (any, error) {
		return "ok", nil
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := s.Get(job.ID)
		if got.Status == JobCompleted {
			if got.Result != "ok" {
				t.Errorf("result = %v", got.Result)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not complete within timeout")
}

func TestJobStore_RunAsyncFailsOnError(t *testing.T) {
	s := NewJobStore()
	job := s.Create("test", "inst1")
	s.RunAsync(job, func(_ context.Context) (any, error) {
		return nil, errors.New("rip")
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := s.Get(job.ID)
		if got.Status == JobFailed {
			if got.Error != "rip" {
				t.Errorf("error = %q", got.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not fail within timeout")
}

func TestJobStore_ListReturnsAll(t *testing.T) {
	s := NewJobStore()
	a := s.Create("a", "i1")
	b := s.Create("b", "i2")
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	gotIDs := map[string]bool{}
	for _, j := range list {
		gotIDs[j.ID] = true
	}
	if !gotIDs[a.ID] || !gotIDs[b.ID] {
		t.Errorf("list missing entries: %v", gotIDs)
	}
}
