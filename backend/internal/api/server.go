package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"konakpro/internal/assign"
	"konakpro/internal/auth"
	"konakpro/internal/booking"
	"konakpro/internal/channel"
	"konakpro/internal/folio"
	"konakpro/internal/housekeeping"
	"konakpro/internal/nightaudit"
	"konakpro/internal/platform/store"
	"konakpro/internal/reports"
)

type Server struct {
	Log          *slog.Logger
	Store        *store.Store
	Auth         *auth.Service
	Booking      *booking.Service
	Folio        *folio.Service
	NightAudit   *nightaudit.Service
	Assign       *assign.Service
	Housekeeping *housekeeping.Service
	Reports      *reports.Service
	Channel      *channel.Service
	Timeout      time.Duration
	DatabaseURL  string
	Registry     *prometheus.Registry
	Metrics      *Metrics

	live *liveHub
}

type Metrics struct {
	duration  *prometheus.HistogramVec
	requests  *prometheus.CounterVec
	delivered prometheus.Counter
	retried   prometheus.Counter
	dead      prometheus.Counter
	audited   prometheus.Counter
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
	m := &Metrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "konakpro_http_request_duration_seconds", Help: "HTTP istek süresi", Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "konakpro_http_requests_total", Help: "HTTP istek sayısı",
		}, []string{"method", "route", "status"}),
		delivered: prometheus.NewCounter(prometheus.CounterOpts{Name: "konakpro_outbox_delivered_total", Help: "Kanala iletilen mesaj"}),
		retried:   prometheus.NewCounter(prometheus.CounterOpts{Name: "konakpro_outbox_retried_total", Help: "Yeniden denenen mesaj"}),
		dead:      prometheus.NewCounter(prometheus.CounterOpts{Name: "konakpro_outbox_dead_total", Help: "Ölü kuyruğa düşen mesaj"}),
		audited:   prometheus.NewCounter(prometheus.CounterOpts{Name: "konakpro_night_audit_items_total", Help: "İşlenen gece denetimi kalemi"}),
	}
	registry.MustRegister(m.duration, m.requests, m.delivered, m.retried, m.dead, m.audited,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) OutboxDelivered() { m.delivered.Inc() }
func (m *Metrics) OutboxRetried()   { m.retried.Inc() }
func (m *Metrics) OutboxDead()      { m.dead.Inc() }
func (m *Metrics) AuditItem()       { m.audited.Inc() }

func (s *Server) Start(ctx context.Context) {
	s.live = newLiveHub(s.DatabaseURL, s.Log)
	go s.live.Run(ctx)
}

func (s *Server) Router() http.Handler {
	if s.live == nil {
		s.live = newLiveHub(s.DatabaseURL, s.Log)
	}
	r := chi.NewRouter()
	r.Use(recoverer(s.Log), observe(s.Log, s.Metrics.duration, s.Metrics.requests))
	r.Get("/health", s.adapt(s.handleHealth))
	r.Handle("/metrics", promhttp.HandlerFor(s.Registry, promhttp.HandlerOpts{}))
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPI)
	})
	for _, route := range s.routes() {
		chain := []func(http.Handler) http.Handler{}
		if !route.stream {
			chain = append(chain, timeout(s.Timeout))
		}
		if !route.public {
			chain = append(chain, s.authorize(route.roles))
		}
		if route.idempotent {
			chain = append(chain, s.idempotent)
		}
		r.With(chain...).Method(route.method, route.path, s.adapt(route.handle))
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, s.Log, req, errRouteNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, s.Log, req, errMethodNotAllowed)
	})
	return r
}

func (s *Server) adapt(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, s.Log, r, err)
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	state, err := s.Store.GetHotelState(ctx)
	if err != nil {
		return writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "database": "unreachable"})
	}
	return writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "database": "ok", "business_date": state.BusinessDate.Format(time.DateOnly),
	})
}
