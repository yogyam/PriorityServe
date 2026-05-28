package handler

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/yourusername/priorityserve/internal/dashboard"
	"github.com/yourusername/priorityserve/internal/scheduler"
)

// ChatHandler accepts POST /v1/chat/completions, pushes the request into the
// priority queue, and waits for the worker result before writing the response.
type ChatHandler struct {
	queue *scheduler.MultiQueue
	dash  *dashboard.Dashboard
}

func NewChatHandler(queue *scheduler.MultiQueue, dash *dashboard.Dashboard) *ChatHandler {
	return &ChatHandler{queue: queue, dash: dash}
}

func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading request body", http.StatusBadRequest)
		return
	}

	priority := scheduler.ParsePriority(r.Header.Get("X-Priority"))
	id := fmt.Sprintf("%016x", rand.Int63())

	req := &scheduler.InferenceRequest{
		ID:         id,
		Priority:   priority,
		Body:       body,
		ResultChan: make(chan scheduler.Result, 1),
		EnqueuedAt: time.Now(),
		Ctx:        r.Context(),
	}

	h.dash.Record(dashboard.Event{
		ID:        id,
		Priority:  priority.String(),
		Kind:      dashboard.KindEnqueued,
		Timestamp: time.Now(),
	})
	log.Printf("id=%s priority=%s enqueued", id, priority)

	if err := h.queue.Push(req); err != nil {
		http.Error(w, "server busy — queue full", http.StatusServiceUnavailable)
		return
	}

	select {
	case result := <-req.ResultChan:
		if result.Err != nil {
			http.Error(w, "backend error: "+result.Err.Error(), http.StatusBadGateway)
			return
		}
		for k, v := range result.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(result.StatusCode)
		w.Write(result.Body)

	case <-r.Context().Done():
		log.Printf("id=%s client disconnected", id)
	}
}
