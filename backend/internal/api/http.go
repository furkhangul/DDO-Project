package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"konakpro/internal/auth"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

const maxBody = 1 << 20

type handler func(w http.ResponseWriter, r *http.Request) error

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, log *slog.Logger, r *http.Request, err error) {
	app := translate(err)
	if app.Status >= 500 {
		log.Error("istek başarısız", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	var body errorBody
	body.Error.Code, body.Error.Message = app.Code, app.Message
	_ = writeJSON(w, app.Status, body)
}

func translate(err error) *apperr.Error {
	if app, ok := apperr.As(err); ok {
		return app
	}
	switch {
	case store.IsExclusion(err):
		return apperr.Conflict("CONSTRAINT_CONFLICT", "Kayıt mevcut bir kayıtla çakışıyor")
	case store.IsUnique(err):
		return apperr.Conflict("DUPLICATE", "Bu kayıt zaten var")
	case store.IsCheck(err):
		if store.ConstraintName(err) == "inventory_not_oversold" {
			return apperr.Conflict("SOLD_OUT", "Müsaitlik yok")
		}
		return apperr.Invalid("Girilen değerler kurallara uymuyor")
	case store.IsForeignKey(err):
		return apperr.Invalid("Bağlı kayıt bulunamadı")
	case store.IsNotFound(err):
		return apperr.NotFound("Kayıt bulunamadı")
	case errors.Is(err, http.ErrHandlerTimeout):
		return apperr.New(http.StatusServiceUnavailable, "TIMEOUT", "İstek zaman aşımına uğradı")
	}
	return apperr.New(http.StatusInternalServerError, "INTERNAL_ERROR", "Beklenmeyen bir hata oluştu")
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return apperr.Invalid("İstek gövdesi boş")
		}
		return apperr.Invalid("İstek gövdesi geçersiz: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Invalid("Geçersiz kimlik")
	}
	return id, nil
}

func parseDate(value, field string) (time.Time, error) {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, apperr.Invalid(field + " YYYY-AA-GG biçiminde olmalı")
	}
	return day, nil
}

func queryRange(r *http.Request) (time.Time, time.Time, error) {
	from, err := parseDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parseDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, apperr.Invalid("to, from tarihinden sonra olmalı")
	}
	if to.Sub(from) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, apperr.Invalid("Tarih aralığı en fazla 400 gün olabilir")
	}
	return from, to, nil
}

type page struct {
	Limit  int32
	Before *int64
}

func parsePage(r *http.Request) (page, error) {
	result := page{Limit: 50}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return result, apperr.Invalid("limit 1 ile 200 arasında olmalı")
		}
		result.Limit = int32(limit)
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor <= 0 {
			return result, apperr.Invalid("cursor geçersiz")
		}
		result.Before = &cursor
	}
	return result, nil
}

type pageResult[T any] struct {
	Items      []T    `json:"items"`
	NextCursor *int64 `json:"next_cursor"`
}

func paged[T any](items []T, limit int32, id func(T) int64) pageResult[T] {
	result := pageResult[T]{Items: items}
	if int32(len(items)) == limit && len(items) > 0 {
		next := id(items[len(items)-1])
		result.NextCursor = &next
	}
	return result
}

func optional(r *http.Request, name string) *string {
	if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
		return &value
	}
	return nil
}

func principalOf(r *http.Request) auth.Principal {
	p, _ := r.Context().Value(principalKey{}).(auth.Principal)
	return p
}

func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return nil, apperr.Invalid("İstek gövdesi okunamadı")
	}
	return body, nil
}

func chiParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}
