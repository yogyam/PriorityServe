package scheduler

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type Priority int

const (
	PriorityLow    Priority = 1
	PriorityMedium Priority = 2
	PriorityHigh   Priority = 3
)

func ParsePriority(s string) Priority {
	switch strings.ToLower(s) {
	case "high":
		return PriorityHigh
	case "low":
		return PriorityLow
	default:
		return PriorityMedium
	}
}

func (p Priority) String() string {
	switch p {
	case PriorityHigh:
		return "high"
	case PriorityMedium:
		return "medium"
	default:
		return "low"
	}
}

// InferenceRequest is the unit of work that travels through the queue → scheduler → worker.
type InferenceRequest struct {
	ID         string
	Priority   Priority
	Body       []byte          // raw JSON body from client (OpenAI format)
	ResultChan chan Result      // worker writes result here; handler reads
	EnqueuedAt time.Time
	Ctx        context.Context // client request context — cancel means client disconnected
}

// Result is what a worker sends back after calling llama.cpp.
type Result struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Err        error
}
