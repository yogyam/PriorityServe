package scheduler

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// ErrQueueFull is returned when a tier's queue is at capacity.
var ErrQueueFull = errors.New("queue full")

// MultiQueue holds three FIFO queues, one per priority tier.
// Pop always drains high before medium, medium before low.
type MultiQueue struct {
	mu     sync.Mutex
	high   []*InferenceRequest
	medium []*InferenceRequest
	low    []*InferenceRequest
	depth  int          // max items per tier
	notify chan struct{} // non-blocking signal: "something was pushed"
}

func NewMultiQueue(depthPerTier int) *MultiQueue {
	return &MultiQueue{
		depth:  depthPerTier,
		notify: make(chan struct{}, 1),
	}
}

// Push adds req to the appropriate tier queue.
// Returns ErrQueueFull if that tier is at capacity.
func (mq *MultiQueue) Push(req *InferenceRequest) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	q := mq.tierQueue(req.Priority)
	if len(*q) >= mq.depth {
		return ErrQueueFull
	}
	*q = append(*q, req)

	// Wake up any blocked Pop. Non-blocking: if notify is already full,
	// Pop will loop and find the item on its next check.
	select {
	case mq.notify <- struct{}{}:
	default:
	}
	return nil
}

// Pop blocks until a request is available, then returns the highest-priority
// pending request. Returns an error only if ctx is cancelled.
func (mq *MultiQueue) Pop(ctx context.Context) (*InferenceRequest, error) {
	for {
		mq.mu.Lock()
		req := mq.popLocked()
		mq.mu.Unlock()

		if req != nil {
			return req, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-mq.notify:
			// loop and try again
		}
	}
}

// Depths returns current length of each tier (for /health and metrics).
func (mq *MultiQueue) Depths() (high, med, low int) {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	return len(mq.high), len(mq.medium), len(mq.low)
}

// popLocked removes and returns the highest-priority available request.
// Must be called with mq.mu held.
func (mq *MultiQueue) popLocked() *InferenceRequest {
	if len(mq.high) > 0 {
		return mq.shift(&mq.high)
	}
	if len(mq.medium) > 0 {
		return mq.shift(&mq.medium)
	}
	if len(mq.low) > 0 {
		return mq.shift(&mq.low)
	}
	return nil
}

func (mq *MultiQueue) tierQueue(p Priority) *[]*InferenceRequest {
	switch p {
	case PriorityHigh:
		return &mq.high
	case PriorityMedium:
		return &mq.medium
	default:
		return &mq.low
	}
}

// shift removes and returns the first element from a slice (FIFO order).
func (mq *MultiQueue) shift(q *[]*InferenceRequest) *InferenceRequest {
	req := (*q)[0]
	(*q)[0] = nil // avoid memory leak
	*q = (*q)[1:]
	return req
}

// AgeResult reports how many promotions happened per transition.
type AgeResult struct {
	LowToMed  int
	MedToHigh int
}

// Age scans the queues and promotes requests that have been waiting longer than
// the given thresholds. Processes medium→high before low→medium so a request
// promoted from low in this tick isn't immediately promoted to high.
func (mq *MultiQueue) Age(lowToMed, medToHigh time.Duration) AgeResult {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	var result AgeResult
	now := time.Now()

	// Promote medium → high.
	var newMedium []*InferenceRequest
	for _, req := range mq.medium {
		if medToHigh > 0 && now.Sub(req.EnqueuedAt) >= medToHigh && len(mq.high) < mq.depth {
			mq.high = append(mq.high, req)
			result.MedToHigh++
			log.Printf("id=%s aged medium→high after %.1fs", req.ID, now.Sub(req.EnqueuedAt).Seconds())
		} else {
			newMedium = append(newMedium, req)
		}
	}
	mq.medium = newMedium

	// Promote low → medium.
	var newLow []*InferenceRequest
	for _, req := range mq.low {
		if lowToMed > 0 && now.Sub(req.EnqueuedAt) >= lowToMed && len(mq.medium) < mq.depth {
			mq.medium = append(mq.medium, req)
			result.LowToMed++
			log.Printf("id=%s aged low→medium after %.1fs", req.ID, now.Sub(req.EnqueuedAt).Seconds())
		} else {
			newLow = append(newLow, req)
		}
	}
	mq.low = newLow

	if result.LowToMed+result.MedToHigh > 0 {
		select {
		case mq.notify <- struct{}{}:
		default:
		}
	}
	return result
}
