package migration

import (
	"context"
	"log/slog"
	"sync"
)

type Publisher interface {
	PublishJob(ctx context.Context, job JobMessage) (string, error)
}

type MemoryPublisher struct {
	mu     sync.Mutex
	jobs   []JobMessage
	logger *slog.Logger
}

func NewStubPublisher(logger *slog.Logger) *MemoryPublisher {
	return &MemoryPublisher{logger: logger}
}

func (p *MemoryPublisher) PublishJob(ctx context.Context, job JobMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.Lock()
	p.jobs = append(p.jobs, job)
	p.mu.Unlock()
	if p.logger != nil {
		p.logger.Info("migration.job.published", "job_id", job.JobID, "migration_id", job.MigrationID)
	}
	return job.JobID, nil
}

func (p *MemoryPublisher) Jobs() []JobMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	jobs := make([]JobMessage, len(p.jobs))
	copy(jobs, p.jobs)
	return jobs
}