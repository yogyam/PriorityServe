package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RequestLatency tracks end-to-end latency (enqueue → done) per priority tier.
	RequestLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "priorityserve_request_latency_seconds",
			Help:    "End-to-end request latency from enqueue to completion, by priority tier.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"priority"},
	)

	// QueueDepth tracks current items waiting in each tier.
	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "priorityserve_queue_depth",
			Help: "Number of requests currently waiting in each priority queue.",
		},
		[]string{"priority"},
	)

	// RequestsTotal counts completed requests by priority and status class.
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "priorityserve_requests_total",
			Help: "Total completed requests by priority tier and HTTP status class.",
		},
		[]string{"priority", "status"},
	)
)
