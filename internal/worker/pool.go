package worker

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yourusername/priorityserve/internal/dashboard"
	"github.com/yourusername/priorityserve/internal/metrics"
	"github.com/yourusername/priorityserve/internal/scheduler"
)

// Pool runs N worker goroutines, each pulling from the shared work channel
// and forwarding requests to the llama.cpp backend.
type Pool struct {
	work          chan *scheduler.InferenceRequest
	backend       *Backend
	dash          *dashboard.Dashboard
	wg            sync.WaitGroup
	activeWorkers atomic.Int32
	totalWorkers  int
}

func NewPool(n int, backend *Backend, dash *dashboard.Dashboard) *Pool {
	p := &Pool{
		// Unbuffered: the scheduler blocks on send until a worker is free.
		// This creates back-pressure so the scheduler never gets ahead of capacity.
		work:         make(chan *scheduler.InferenceRequest),
		backend:      backend,
		dash:         dash,
		totalWorkers: n,
	}
	for range n {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

func (p *Pool) ActiveWorkers() int { return int(p.activeWorkers.Load()) }
func (p *Pool) TotalWorkers() int  { return p.totalWorkers }

// WorkChan returns the send side of the work channel.
// The scheduler writes here; the pool's workers read from it.
func (p *Pool) WorkChan() chan<- *scheduler.InferenceRequest {
	return p.work
}

// Shutdown drains in-flight requests and stops all workers.
func (p *Pool) Shutdown() {
	close(p.work)
	p.wg.Wait()
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for req := range p.work {
		p.activeWorkers.Add(1)
		p.dash.Record(dashboard.Event{
			ID:        req.ID,
			Priority:  req.Priority.String(),
			Kind:      dashboard.KindActive,
			Timestamp: time.Now(),
		})
		log.Printf("id=%s priority=%s worker start", req.ID, req.Priority)

		result := p.backend.Do(req)

		latency := time.Since(req.EnqueuedAt)
		p.activeWorkers.Add(-1)
		p.dash.Record(dashboard.Event{
			ID:        req.ID,
			Priority:  req.Priority.String(),
			Kind:      dashboard.KindDone,
			LatencyMs: float64(latency.Milliseconds()),
			Status:    result.StatusCode,
			Timestamp: time.Now(),
		})

		tier := req.Priority.String()
		metrics.RequestLatency.WithLabelValues(tier).Observe(latency.Seconds())
		metrics.RequestsTotal.WithLabelValues(tier, statusClass(result.StatusCode)).Inc()

		log.Printf("id=%s priority=%s worker done status=%d latency=%.0fms",
			req.ID, req.Priority, result.StatusCode, float64(latency.Milliseconds()))

		req.ResultChan <- result
	}
}

func statusClass(code int) string {
	return fmt.Sprintf("%dxx", code/100)
}
