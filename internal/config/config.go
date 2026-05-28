package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr     string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	BackendURL     string
	BackendTimeout time.Duration
	WorkerCount    int
	QueueDepth     int
	CacheSize      int
	MetricsAddr    string
}

func Load() *Config {
	return &Config{
		ListenAddr:     getEnv("PS_LISTEN_ADDR", ":8080"),
		ReadTimeout:    getDuration("PS_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:   getDuration("PS_WRITE_TIMEOUT", 120*time.Second),
		BackendURL:     getEnv("PS_BACKEND_URL", "http://localhost:8081"),
		BackendTimeout: getDuration("PS_BACKEND_TIMEOUT", 300*time.Second),
		WorkerCount:    getInt("PS_WORKER_COUNT", 2),
		QueueDepth:     getInt("PS_QUEUE_DEPTH", 100),
		CacheSize:      getInt("PS_CACHE_SIZE", 128),
		MetricsAddr:    getEnv("PS_METRICS_ADDR", ":9090"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
