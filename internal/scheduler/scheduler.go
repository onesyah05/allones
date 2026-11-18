// Package scheduler runs cron jobs: at each due time it starts an unattended
// agent turn with the job's prompt and role.
package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/cron"
	"github.com/allones/allones/internal/store"
)

type Scheduler struct {
	store  *store.Store
	engine *agent.Engine
	stop   chan struct{}
	mu     sync.Mutex
	wg     sync.WaitGroup
}

func New(st *store.Store, eng *agent.Engine) *Scheduler {
	return &Scheduler{store: st, engine: eng, stop: make(chan struct{})}
}

func (s *Scheduler) NextRuns(expr string, n int) ([]int64, error) {
	return cron.NextRuns(expr, n)
}

func (s *Scheduler) Start() {
	// Compute next-run for jobs that don't have one.
	jobs, _ := s.store.ListCron()
	for i := range jobs {
		if jobs[i].Enabled && jobs[i].NextRun == 0 {
			if runs, err := cron.NextRuns(jobs[i].Schedule, 1); err == nil && len(runs) > 0 {
				jobs[i].NextRun = runs[0]
				s.store.SaveCron(&jobs[i])
			}
		}
	}
	s.wg.Add(1)
	go s.loop()
}

func (s *Scheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scheduler) tick() {
	now := time.Now().UnixMilli()
	jobs, err := s.store.ListCron()
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		if !j.Enabled || j.NextRun == 0 || j.NextRun > now {
			continue
		}
		go s.runJob(j)
	}
}

func (s *Scheduler) runJob(j store.CronJob) {
	// Advance next-run immediately so a long job is not double-started.
	sch, err := cron.Parse(j.Schedule)
	if err != nil {
		j.Enabled = false
		j.LastStatus = "bad schedule: " + err.Error()
		s.store.SaveCron(&j)
		return
	}
	next := sch.Next(time.Now())
	j.NextRun = next.UnixMilli()
	j.LastRun = time.Now().UnixMilli()
	s.store.SaveCron(&j)

	sess := &store.Session{Role: j.Role, Model: j.Model, Source: "cron", Title: "cron: " + j.Name}
	s.store.CreateSession(sess)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s.store.Audit("cron:"+j.ID, "cron.run", j.Name, j.Prompt, "info")
	err = s.engine.Run(ctx, agent.Request{
		SessionID: sess.ID, Text: j.Prompt, Role: j.Role, Model: j.Model,
		Source: "cron", Interactive: false, AutoApprove: j.AutoApprove,
	}, nil)
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	fresh, _ := s.findJob(j.ID)
	if fresh != nil {
		fresh.LastStatus = status
		fresh.LastSession = sess.ID
		s.store.SaveCron(fresh)
	}
}

func (s *Scheduler) findJob(id string) (*store.CronJob, error) {
	jobs, err := s.store.ListCron()
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i], nil
		}
	}
	return nil, nil
}
