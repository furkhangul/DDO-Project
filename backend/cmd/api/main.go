package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"konakpro/internal/api"
	"konakpro/internal/assign"
	"konakpro/internal/auth"
	"konakpro/internal/booking"
	"konakpro/internal/channel"
	"konakpro/internal/db/migrations"
	"konakpro/internal/folio"
	"konakpro/internal/housekeeping"
	"konakpro/internal/nightaudit"
	"konakpro/internal/platform/config"
	"konakpro/internal/platform/store"
	"konakpro/internal/reports"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("sunucu durdu", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return migrations.Up(cfg.DatabaseURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL, cfg.MaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool)

	registry := prometheus.NewRegistry()
	metrics := api.NewMetrics(registry)
	server := &api.Server{
		Log:          log,
		Store:        st,
		Auth:         auth.New(st, cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL),
		Booking:      &booking.Service{Store: st},
		Folio:        &folio.Service{Store: st},
		NightAudit:   &nightaudit.Service{Store: st, Log: log, AllowFaults: cfg.FaultInjection, OnItemCharged: metrics.AuditItem},
		Assign:       &assign.Service{Store: st, Seed: cfg.SolverSeed},
		Housekeeping: &housekeeping.Service{Store: st},
		Reports:      &reports.Service{Store: st},
		Channel:      &channel.Service{Store: st},
		Timeout:      cfg.RequestTimeout,
		DatabaseURL:  cfg.DatabaseURL,
		Registry:     registry,
		Metrics:      metrics,
	}
	server.Start(ctx)

	worker := &channel.Worker{
		Store: st, Log: log, Client: &http.Client{Timeout: 3 * time.Second}, BaseURL: cfg.ChannelARIURL,
		Interval: cfg.OutboxInterval, Batch: cfg.OutboxBatch, MaxAttempts: cfg.OutboxMaxAttempt,
		Backoff: cfg.OutboxBackoff, MaxBackoff: cfg.OutboxMaxBackoff, Observer: metrics,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	failed := make(chan error, 1)
	go func() {
		log.Info("api dinliyor", "addr", cfg.Addr)
		failed <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	log.Info("kapanış başladı")
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}
