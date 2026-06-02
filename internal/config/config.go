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
	AgeLowToMed    time.Duration // promote low→medium after this wait (0 = disabled)
	AgeMedToHigh   time.Duration // promote medium→high after this wait (0 = disabled)
	AgeInterval    time.Duration // how often to scan for aged requests
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
		AgeLowToMed:    getDuration("PS_AGE_LOW_TO_MED", 30*time.Second),
		AgeMedToHigh:   getDuration("PS_AGE_MED_TO_HIGH", 60*time.Second),
		AgeInterval:    getDuration("PS_AGE_INTERVAL", 1*time.Second),
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
