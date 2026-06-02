package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yourusername/priorityserve/internal/config"
	"github.com/yourusername/priorityserve/internal/dashboard"
	"github.com/yourusername/priorityserve/internal/handler"
	"github.com/yourusername/priorityserve/internal/metrics"
	"github.com/yourusername/priorityserve/internal/scheduler"
	"github.com/yourusername/priorityserve/internal/worker"
)

func main() {
	cfg := config.Load()

	backend := worker.NewBackend(cfg.BackendURL)
	if err := waitForBackend(backend); err != nil {
		log.Fatalf("startup failed: %v", err)
	}

	dash := dashboard.New()
	queue := scheduler.NewMultiQueue(cfg.QueueDepth)
	pool := worker.NewPool(cfg.WorkerCount, backend, dash)
	sched := scheduler.NewScheduler(queue, pool.WorkChan())
	ui := handler.NewUIHandler(dash, queue, pool)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handler.Health)
	mux.Handle("/v1/chat/completions", handler.NewChatHandler(queue, dash))
	mux.HandleFunc("/ui", ui.Page)
	mux.HandleFunc("/ui/events", ui.Events)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go sched.Run(ctx)

	// Expose Prometheus metrics on a separate port so it doesn't mix with the API.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: metricsMux}
	go func() {
		log.Printf("Metrics listening on %s/metrics", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	// Keep queue depth gauges in sync with the live queue state.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h, m, l := queue.Depths()
				metrics.QueueDepth.WithLabelValues("high").Set(float64(h))
				metrics.QueueDepth.WithLabelValues("medium").Set(float64(m))
				metrics.QueueDepth.WithLabelValues("low").Set(float64(l))
			}
		}
	}()

	// Aging: promote requests that have waited too long to the next tier.
	log.Printf("aging: low→medium after %s, medium→high after %s",
		cfg.AgeLowToMed, cfg.AgeMedToHigh)
	go func() {
		t := time.NewTicker(cfg.AgeInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				result := queue.Age(cfg.AgeLowToMed, cfg.AgeMedToHigh)
				if result.LowToMed > 0 {
					metrics.PromotionsTotal.WithLabelValues("low", "medium").Add(float64(result.LowToMed))
				}
				if result.MedToHigh > 0 {
					metrics.PromotionsTotal.WithLabelValues("medium", "high").Add(float64(result.MedToHigh))
				}
			}
		}
	}()

	go func() {
		log.Printf("PriorityServe listening on %s (backend: %s, workers: %d)",
			cfg.ListenAddr, cfg.BackendURL, cfg.WorkerCount)
		log.Printf("Dashboard: http://localhost%s/ui", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	metricsSrv.Shutdown(shutCtx)
	cancel()
	pool.Shutdown()
	log.Println("stopped")
}

func waitForBackend(b *worker.Backend) error {
	for i := range 10 {
		if err := b.HealthCheck(); err == nil {
			return nil
		}
		log.Printf("waiting for llama.cpp backend... (%d/10)", i+1)
		time.Sleep(2 * time.Second)
	}
	return b.HealthCheck()
}
