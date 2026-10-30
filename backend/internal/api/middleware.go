package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type principalKey struct{}

type recorder struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.body != nil {
		r.body.Write(p)
	}
	return r.ResponseWriter.Write(p)
}

func (r *recorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func observe(log *slog.Logger, duration *prometheus.HistogramVec, requests *prometheus.CounterVec) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			requestID := uuid.NewString()
			w.Header().Set("X-Request-Id", requestID)
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			pattern := chi.RouteContext(r.Context()).RoutePattern()
			if pattern == "" {
				pattern = "unmatched"
			}
			elapsed := time.Since(started)
			duration.WithLabelValues(r.Method, pattern).Observe(elapsed.Seconds())
			requests.WithLabelValues(r.Method, pattern, strconv.Itoa(rec.status)).Inc()
			if pattern != "/metrics" && pattern != "/health" {
				log.Info("istek", "request_id", requestID, "method", r.Method, "route", pattern, "status", rec.status, "duration_ms", elapsed.Milliseconds())
			}
		})
	}
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if value := recover(); value != nil {
					if value == http.ErrAbortHandler {
						panic(value)
					}
					log.Error("panik", "value", value, "path", r.URL.Path)
					writeError(w, log, r, apperr.New(http.StatusInternalServerError, "INTERNAL_ERROR", "Beklenmeyen bir hata oluştu"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func timeout(limit time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), limit)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *Server) authorize(roles []db.UserRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, found := strings.CutPrefix(header, "Bearer ")
			if !found {
				writeError(w, s.Log, r, apperr.Unauthorized("Oturum gerekli"))
				return
			}
			principal, err := s.Auth.Parse(token)
			if err != nil {
				writeError(w, s.Log, r, err)
				return
			}
			if !allowed(roles, principal.Role) {
				writeError(w, s.Log, r, apperr.Forbidden())
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
		})
	}
}

func allowed(roles []db.UserRole, role db.UserRole) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func (s *Server) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 128 {
			writeError(w, s.Log, r, apperr.Invalid("Idempotency-Key en fazla 128 karakter olabilir"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeError(w, s.Log, r, apperr.Invalid("İstek gövdesi okunamadı"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		user := principalOf(r).UserID
		digest := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))

		_, err = s.Store.ClaimIdempotencyKey(r.Context(), db.ClaimIdempotencyKeyParams{
			UserID: user, IdemKey: key, Method: r.Method, Path: r.URL.Path, RequestHash: digest[:],
		})
		if store.IsNotFound(err) {
			s.replay(w, r, user, key, digest[:])
			return
		}
		if err != nil {
			writeError(w, s.Log, r, err)
			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK, body: &bytes.Buffer{}}
		next.ServeHTTP(rec, r)

		finish, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if rec.status >= 500 {
			if err := s.Store.ReleaseIdempotencyKey(finish, db.ReleaseIdempotencyKeyParams{UserID: user, IdemKey: key}); err != nil {
				s.Log.Error("idempotency anahtarı bırakılamadı", "error", err)
			}
			return
		}
		status := int32(rec.status)
		if err := s.Store.FinishIdempotencyKey(finish, db.FinishIdempotencyKeyParams{
			UserID: user, IdemKey: key, StatusCode: &status, ResponseBody: rec.body.Bytes(),
		}); err != nil {
			s.Log.Error("idempotency sonucu kaydedilemedi", "error", err)
		}
	})
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request, user int64, key string, digest []byte) {
	stored, err := s.Store.GetIdempotencyKey(r.Context(), db.GetIdempotencyKeyParams{UserID: user, IdemKey: key})
	if err != nil {
		writeError(w, s.Log, r, err)
		return
	}
	if !bytes.Equal(stored.RequestHash, digest) {
		writeError(w, s.Log, r, apperr.New(http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED", "Bu anahtar farklı bir istekle kullanılmış"))
		return
	}
	if stored.StatusCode == nil {
		writeError(w, s.Log, r, apperr.Conflict("IDEMPOTENT_IN_PROGRESS", "Aynı istek hâlâ işleniyor"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(int(*stored.StatusCode))
	_, _ = w.Write(stored.ResponseBody)
}
