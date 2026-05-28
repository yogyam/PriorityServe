package scheduler

import (
	"context"
	"log"
)

// Scheduler is a single goroutine that drains the MultiQueue and forwards
// requests to the worker pool in strict priority order.
type Scheduler struct {
	queue *MultiQueue
	work  chan<- *InferenceRequest
}

func NewScheduler(queue *MultiQueue, work chan<- *InferenceRequest) *Scheduler {
	return &Scheduler{queue: queue, work: work}
}

// Run blocks until ctx is cancelled. Call it in a goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		req, err := s.queue.Pop(ctx)
		if err != nil {
			return // ctx cancelled — server shutting down
		}

		// Drop requests whose client already disconnected while waiting in queue.
		select {
		case <-req.Ctx.Done():
			log.Printf("id=%s priority=%s dropped: client disconnected in queue", req.ID, req.Priority)
			req.ResultChan <- Result{StatusCode: 408, Err: req.Ctx.Err()}
			continue
		default:
		}

		log.Printf("id=%s priority=%s dispatching to worker", req.ID, req.Priority)

		// Send to pool — blocks until a worker slot is free (back-pressure).
		select {
		case s.work <- req:
		case <-ctx.Done():
			return
		}
	}
}
