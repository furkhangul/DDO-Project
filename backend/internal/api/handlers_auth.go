package api

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"konakpro/internal/channel"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Email == "" || body.Password == "" {
		return apperr.Invalid("E-posta ve parola zorunlu")
	}
	tokens, user, err := s.Auth.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "user": userView(user)})
}

func userView(u db.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "full_name": u.FullName, "role": u.Role}
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	tokens, err := s.Auth.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := s.Store.InTx(r.Context(), func(q *db.Queries) error {
		return s.Auth.Logout(r.Context(), q, body.RefreshToken)
	}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	user, err := s.Store.GetUserByID(r.Context(), principalOf(r).UserID)
	if err != nil {
		return apperr.Unauthorized("Kullanıcı etkin değil")
	}
	return writeJSON(w, http.StatusOK, userView(user))
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.ListStaff(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) error {
	state, err := s.Store.GetHotelState(r.Context())
	if err != nil {
		return err
	}
	var (
		counts     db.DashboardCountsRow
		arrivals   []db.ListArrivalsRow
		departures []db.ListDeparturesRow
		recent     []db.RecentActivityRow
	)
	group, ctx := errgroup.WithContext(r.Context())
	group.Go(func() (err error) {
		counts, err = s.Store.DashboardCounts(ctx, state.BusinessDate)
		return err
	})
	group.Go(func() (err error) {
		arrivals, err = s.Store.ListArrivals(ctx, state.BusinessDate)
		return err
	})
	group.Go(func() (err error) {
		departures, err = s.Store.ListDepartures(ctx, state.BusinessDate)
		return err
	})
	group.Go(func() (err error) {
		recent, err = s.Store.RecentActivity(ctx)
		return err
	})
	if err := group.Wait(); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"recent": recent, "hotel": state.Name, "business_date": state.BusinessDate.Format(time.DateOnly),
		"counts": counts, "arrivals": arrivals, "departures": departures,
	})
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePage(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.ListAudit(r.Context(), db.ListAuditParams{Limit: p.Limit, BeforeID: p.Before, Entity: optional(r, "entity")})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, paged(rows, p.Limit, func(row db.AuditLog) int64 { return row.ID }))
}

func (s *Server) channelEvent(w http.ResponseWriter, r *http.Request) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	result, err := s.Channel.Ingest(r.Context(), chiParam(r, "code"), r.Header.Get("X-Channel-Signature"), body)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if result.Outcome == channel.OutcomeApplied {
		status = http.StatusAccepted
	}
	return writeJSON(w, status, result)
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.ListChannels(r.Context())
	if err != nil {
		return err
	}
	inbox, err := s.Store.InboxStats(r.Context())
	if err != nil {
		return err
	}
	outbox, err := s.Store.OutboxStats(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": rows, "inbox": inbox, "outbox": outbox})
}

func (s *Server) listInbox(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePage(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.ListInbox(r.Context(), db.ListInboxParams{Limit: p.Limit, BeforeID: p.Before})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, paged(rows, p.Limit, func(row db.ListInboxRow) int64 { return row.ID }))
}

func (s *Server) listOutbox(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePage(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.ListOutbox(r.Context(), db.ListOutboxParams{Limit: p.Limit, BeforeID: p.Before, Status: optional(r, "status")})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, paged(rows, p.Limit, func(row db.ListOutboxRow) int64 { return row.ID }))
}

func (s *Server) retryOutbox(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	changed, err := s.Store.RetryDeadOutbox(r.Context(), id)
	if err != nil {
		return err
	}
	if changed == 0 {
		return apperr.Conflict("NOT_DEAD", "Mesaj ölü kuyrukta değil veya bekleyen güncel bir mesaj var")
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "pending"})
}

func (s *Server) auditStatus(w http.ResponseWriter, r *http.Request) error {
	state, current, history, err := s.NightAudit.Status(r.Context())
	if err != nil {
		return err
	}
	violations, err := s.Store.FolioIntegrityViolations(r.Context())
	if err != nil {
		return err
	}
	duplicates, err := s.Store.DuplicateNightCharges(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"business_date": state.BusinessDate.Format(time.DateOnly), "current": current, "history": history,
		"integrity_violations": violations, "duplicate_charges": duplicates,
	})
}

func detached(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}
