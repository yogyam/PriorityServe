package scheduler

import (
	"context"
	"errors"
	"sync"
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
