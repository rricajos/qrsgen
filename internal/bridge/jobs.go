package bridge

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// JobStatus es el estado de un job async.
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

// Job representa una tarea async (típicamente bulk history import).
// El resultado se obtiene vía polling al endpoint `/jobs/:id`.
type Job struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`     // "bulk_history_import" | "reconcile_saved_contacts" | etc.
	Instance  string    `json:"instance"` // instance asociada
	Status    JobStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Result contiene el resultado JSON del job una vez completado.
	// Tipos esperados según `Type`: BulkImportResult, ReconcileResult, etc.
	Result any `json:"result,omitempty"`
}

// JobStore almacena jobs in-memory con TTL. Suficiente para
// bulk imports + reconciles que tardan minutos. Sin persistencia
// porque el job se considera disposable — si qrsgen reinicia, el
// cliente reintenta. v0.52.0.
type JobStore struct {
	mu sync.Mutex

	jobs map[string]*Job
	ttl  time.Duration // jobs > ttl se purgan
}

// NewJobStore crea un store con TTL de 24h (jobs completados se
// limpian después de 24h).
func NewJobStore() *JobStore {
	js := &JobStore{
		jobs: make(map[string]*Job),
		ttl:  24 * time.Hour,
	}
	// Cleanup ticker (cada 1h)
	go js.cleanupLoop()
	return js
}

// Create registra un job nuevo en estado pending. Devuelve el ID.
func (s *JobStore) Create(jobType, instance string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := &Job{
		ID:        uuid.NewString(),
		Type:      jobType,
		Instance:  instance,
		Status:    JobPending,
		CreatedAt: time.Now(),
	}
	s.jobs[job.ID] = job
	return job
}

// Start marca el job como running.
func (s *JobStore) Start(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobRunning
		j.StartedAt = time.Now()
	}
}

// Complete marca el job como completed con el result dado.
func (s *JobStore) Complete(id string, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobCompleted
		j.EndedAt = time.Now()
		j.Result = result
	}
}

// Fail marca el job como failed con el error msg.
func (s *JobStore) Fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobFailed
		j.EndedAt = time.Now()
		if err != nil {
			j.Error = err.Error()
		}
	}
}

// Get devuelve un snapshot del job (copia para no exponer la
// referencia mutable).
func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	c := *j
	return &c, true
}

// List devuelve snapshots de todos los jobs vivos (no purgados).
// Útil para un endpoint admin de troubleshooting.
func (s *JobStore) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

// cleanupLoop purga jobs completados/failed más viejos que TTL.
func (s *JobStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, j := range s.jobs {
			if j.Status != JobCompleted && j.Status != JobFailed {
				continue
			}
			ref := j.EndedAt
			if ref.IsZero() {
				ref = j.CreatedAt
			}
			if now.Sub(ref) > s.ttl {
				delete(s.jobs, id)
			}
		}
		s.mu.Unlock()
	}
}

// RunAsync arranca una función en goroutine, manejando los estados
// Start/Complete/Fail del job. fn puede usar el ctx del job (no
// del request HTTP que iniciado la operación — el request termina
// pronto pero la operación sigue en background).
func (s *JobStore) RunAsync(job *Job, fn func(ctx context.Context) (any, error)) {
	go func() {
		s.Start(job.ID)
		ctx := context.Background()
		result, err := fn(ctx)
		if err != nil {
			s.Fail(job.ID, err)
			return
		}
		s.Complete(job.ID, result)
	}()
}
