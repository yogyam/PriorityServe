package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestHighBeforeLow(t *testing.T) {
	q := NewMultiQueue(100)

	// Push 10 low-priority requests first.
	for range 10 {
		q.Push(&InferenceRequest{Priority: PriorityLow, EnqueuedAt: time.Now()})
	}
	// Then push 1 high-priority request.
	q.Push(&InferenceRequest{Priority: PriorityHigh, EnqueuedAt: time.Now()})

	ctx := context.Background()

	// First pop must be the high-priority one, regardless of arrival order.
	got, err := q.Pop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != PriorityHigh {
		t.Fatalf("expected PriorityHigh first, got %v", got.Priority)
	}

	// Remaining 10 must all be low.
	for i := range 10 {
		got, err = q.Pop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.Priority != PriorityLow {
			t.Fatalf("item %d: expected PriorityLow, got %v", i, got.Priority)
		}
	}
}

func TestTierOrder(t *testing.T) {
	q := NewMultiQueue(100)

	q.Push(&InferenceRequest{Priority: PriorityLow, EnqueuedAt: time.Now()})
	q.Push(&InferenceRequest{Priority: PriorityMedium, EnqueuedAt: time.Now()})
	q.Push(&InferenceRequest{Priority: PriorityHigh, EnqueuedAt: time.Now()})

	ctx := context.Background()
	want := []Priority{PriorityHigh, PriorityMedium, PriorityLow}
	for i, w := range want {
		got, _ := q.Pop(ctx)
		if got.Priority != w {
			t.Errorf("pop %d: want %v, got %v", i, w, got.Priority)
		}
	}
}

func TestFIFOWithinTier(t *testing.T) {
	q := NewMultiQueue(100)

	t0 := time.Now()
	for i := range 5 {
		q.Push(&InferenceRequest{
			ID:         string(rune('A' + i)),
			Priority:   PriorityMedium,
			EnqueuedAt: t0.Add(time.Duration(i) * time.Millisecond),
		})
	}

	ctx := context.Background()
	for i := range 5 {
		got, _ := q.Pop(ctx)
		want := string(rune('A' + i))
		if got.ID != want {
			t.Errorf("pop %d: want ID=%s, got %s", i, want, got.ID)
		}
	}
}

func TestQueueFull(t *testing.T) {
	q := NewMultiQueue(2)
	q.Push(&InferenceRequest{Priority: PriorityLow})
	q.Push(&InferenceRequest{Priority: PriorityLow})

	err := q.Push(&InferenceRequest{Priority: PriorityLow})
	if err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

func TestPopBlocksThenUnblocks(t *testing.T) {
	q := NewMultiQueue(10)
	ctx := context.Background()

	result := make(chan *InferenceRequest, 1)
	go func() {
		req, _ := q.Pop(ctx)
		result <- req
	}()

	time.Sleep(20 * time.Millisecond) // let goroutine block
	q.Push(&InferenceRequest{Priority: PriorityHigh, EnqueuedAt: time.Now()})

	select {
	case got := <-result:
		if got.Priority != PriorityHigh {
			t.Fatalf("expected PriorityHigh, got %v", got.Priority)
		}
	case <-time.After(time.Second):
		t.Fatal("Pop did not unblock after Push")
	}
}

func TestPopCancelledContext(t *testing.T) {
	q := NewMultiQueue(10)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := q.Pop(ctx)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("Pop did not return after context cancel")
	}
}
