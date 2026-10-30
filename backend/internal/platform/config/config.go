package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr             string
	DatabaseURL      string
	MaxConns         int32
	JWTSecret        string
	AccessTTL        time.Duration
	RefreshTTL       time.Duration
	RequestTimeout   time.Duration
	ChannelARIURL    string
	OutboxInterval   time.Duration
	OutboxBatch      int32
	OutboxMaxAttempt int32
	OutboxBackoff    time.Duration
	OutboxMaxBackoff time.Duration
	FaultInjection   bool
	SolverSeed       int64
}

func Load() (Config, error) {
	cfg := Config{
		Addr:             env("API_ADDR", ":8080"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		ChannelARIURL:    env("CHANNEL_ARI_URL", "http://simulator:9090"),
		FaultInjection:   os.Getenv("FAULT_INJECTION") == "true",
		MaxConns:         int32(intEnv("DB_MAX_CONNS", 30)),
		OutboxBatch:      int32(intEnv("OUTBOX_BATCH", 50)),
		OutboxMaxAttempt: int32(intEnv("OUTBOX_MAX_ATTEMPTS", 6)),
		SolverSeed:       int64(intEnv("SOLVER_SEED", 42)),
		AccessTTL:        durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTTL:       durationEnv("REFRESH_TOKEN_TTL", 7*24*time.Hour),
		RequestTimeout:   durationEnv("REQUEST_TIMEOUT", 20*time.Second),
		OutboxInterval:   durationEnv("OUTBOX_INTERVAL", time.Second),
		OutboxBackoff:    durationEnv("OUTBOX_BACKOFF", 500*time.Millisecond),
		OutboxMaxBackoff: durationEnv("OUTBOX_MAX_BACKOFF", time.Minute),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL zorunlu")
	}
	if len(cfg.JWTSecret) < 32 {
		return cfg, fmt.Errorf("JWT_SECRET en az 32 karakter olmalı")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return value
	}
	return fallback
}
