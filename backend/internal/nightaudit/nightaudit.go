package nightaudit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

const advisoryKey = 7244001

var ErrInjected = errors.New("gece denetimi hata enjeksiyonu")

type Fault struct {
	CrashAfter int
	HardExit   bool
	ItemDelay  time.Duration
}

type Service struct {
	Store         *store.Store
	Log           *slog.Logger
	AllowFaults   bool
	OnItemCharged func()
}

type Report struct {
	BusinessDate string `json:"business_date"`
	NextDate     string `json:"next_business_date"`
	Charged      int    `json:"charged"`
	NoShows      int    `json:"no_shows"`
	Attempts     int32  `json:"attempts"`
	Resumed      bool   `json:"resumed"`
}

func (s *Service) Run(ctx context.Context, actor auditlog.Actor, fault Fault) (Report, error) {
	if !s.AllowFaults {
		fault = Fault{}
	}
	conn, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return Report{}, err
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryKey).Scan(&locked); err != nil {
		return Report{}, err
	}
	if !locked {
		return Report{}, apperr.Conflict("AUDIT_RUNNING", "Gece denetimi zaten çalışıyor")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.Exec(release, "SELECT pg_advisory_unlock($1)", advisoryKey); err != nil {
			conn.Conn().Close(release)
		}
	}()

	state, err := s.Store.GetHotelState(ctx)
	if err != nil {
		return Report{}, err
	}
	day := state.BusinessDate
	run, err := s.Store.StartAuditRun(ctx, day)
	if err != nil {
		return Report{}, err
	}
	if _, err := s.Store.SeedAuditItems(ctx, day); err != nil {
		return Report{}, err
	}
	if err := s.Store.SetAuditTotals(ctx, day); err != nil {
		return Report{}, err
	}
	pending, err := s.Store.PendingAuditItems(ctx, day)
	if err != nil {
		return Report{}, err
	}

	report := Report{BusinessDate: day.Format(time.DateOnly), Attempts: run.Attempts, Resumed: run.Attempts > 1}
	for i, id := range pending {
		if fault.CrashAfter > 0 && i == fault.CrashAfter {
			if fault.HardExit {
				s.Log.Error("hata enjeksiyonu: süreç sonlandırılıyor", "after", i)
				os.Exit(137)
			}
			if err := s.Store.FailAuditRun(ctx, db.FailAuditRunParams{BusinessDate: day, LastError: strPtr(ErrInjected.Error())}); err != nil {
				return report, err
			}
			return report, ErrInjected
		}
		if fault.ItemDelay > 0 {
			time.Sleep(fault.ItemDelay)
		}
		if err := s.chargeStay(ctx, day, id); err != nil {
			message := err.Error()
			if failErr := s.Store.FailAuditRun(ctx, db.FailAuditRunParams{BusinessDate: day, LastError: &message}); failErr != nil {
				s.Log.Error("denetim hatası kaydedilemedi", "error", failErr)
			}
			return report, fmt.Errorf("rezervasyon %d: %w", id, err)
		}
		report.Charged++
		if s.OnItemCharged != nil {
			s.OnItemCharged()
		}
	}

	noShows, err := s.processNoShows(ctx, actor, day)
	if err != nil {
		return report, err
	}
	report.NoShows = noShows

	next, err := s.finish(ctx, actor, day)
	if err != nil {
		return report, err
	}
	report.NextDate = next.Format(time.DateOnly)
	return report, nil
}

func (s *Service) chargeStay(ctx context.Context, day time.Time, id int64) error {
	return s.Store.InTx(ctx, func(q *db.Queries) error {
		r, err := q.LockReservation(ctx, id)
		if err != nil {
			return err
		}
		if r.Status == db.ReservationStatusCheckedIn {
			if err := postNight(ctx, q, r, day); err != nil {
				return err
			}
		}
		if err := q.CompleteAuditItem(ctx, db.CompleteAuditItemParams{BusinessDate: day, ReservationID: id}); err != nil {
			return err
		}
		return q.BumpAuditProgress(ctx, day)
	})
}

func postNight(ctx context.Context, q *db.Queries, r db.Reservation, day time.Time) error {
	folio, err := q.PrimaryFolio(ctx, r.ID)
	if err != nil {
		return err
	}
	nights, err := q.ListReservationNights(ctx, r.ID)
	if err != nil {
		return err
	}
	for _, n := range nights {
		if !n.StayDate.Equal(day) {
			continue
		}
		stamp := day.Format("02.01.2006")
		for _, line := range []struct {
			kind   db.FolioLineKind
			amount int64
			label  string
		}{
			{db.FolioLineKindRoom, n.NetKurus, "Oda ücreti " + stamp},
			{db.FolioLineKindTaxKdv, n.KdvKurus, "KDV " + stamp},
			{db.FolioLineKindTaxAccom, n.AccomKurus, "Konaklama vergisi " + stamp},
		} {
			if line.amount == 0 {
				continue
			}
			if _, err := q.InsertNightCharge(ctx, db.InsertNightChargeParams{
				FolioID: folio.ID, ReservationID: r.ID, Kind: line.kind, AmountKurus: line.amount,
				Description: line.label, BusinessDate: day,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) processNoShows(ctx context.Context, actor auditlog.Actor, day time.Time) (int, error) {
	rows, err := s.Store.ListNoShows(ctx, day)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		err := s.Store.InTx(ctx, func(q *db.Queries) error {
			r, err := q.LockReservation(ctx, row.ID)
			if err != nil {
				return err
			}
			if r.Status != db.ReservationStatusConfirmed {
				return nil
			}
			if err := booking.ReleaseNights(ctx, q, r.RoomTypeID, r.CheckIn, r.CheckOut); err != nil {
				return err
			}
			if err := q.DeleteAssignments(ctx, r.ID); err != nil {
				return err
			}
			if err := q.UpdateReservationStatus(ctx, db.UpdateReservationStatusParams{ID: r.ID, Status: db.ReservationStatusNoShow}); err != nil {
				return err
			}
			count++
			return auditlog.Write(ctx, q, actor, "reservation.no_show", "reservation", r.ID, map[string]string{"status": "confirmed"}, map[string]string{"status": "no_show"})
		})
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func (s *Service) finish(ctx context.Context, actor auditlog.Actor, day time.Time) (time.Time, error) {
	var next time.Time
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		if err := q.UpsertDailyStats(ctx, day); err != nil {
			return err
		}
		tomorrow := day.AddDate(0, 0, 1)
		rooms, err := q.ListInHouseRooms(ctx, tomorrow)
		if err != nil {
			return err
		}
		for _, room := range rooms {
			if _, err := q.UpsertTask(ctx, db.UpsertTaskParams{
				RoomID: room, TaskType: "konaklama_temizligi", BusinessDate: tomorrow,
			}); err != nil {
				return err
			}
		}
		if err := q.FinishAuditRun(ctx, day); err != nil {
			return err
		}
		next, err = q.AdvanceBusinessDate(ctx, day)
		if err != nil {
			return err
		}
		return auditlog.WriteRef(ctx, q, actor, "night_audit.completed", "night_audit", day.Format(time.DateOnly), nil,
			map[string]string{"next_business_date": next.Format(time.DateOnly)})
	})
	return next, err
}

func (s *Service) Status(ctx context.Context) (db.HotelState, *db.NightAuditRun, []db.NightAuditRun, error) {
	state, err := s.Store.GetHotelState(ctx)
	if err != nil {
		return db.HotelState{}, nil, nil, err
	}
	history, err := s.Store.ListAuditRuns(ctx)
	if err != nil {
		return state, nil, nil, err
	}
	current, err := s.Store.GetAuditRun(ctx, state.BusinessDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil, history, nil
	}
	if err != nil {
		return state, nil, nil, err
	}
	return state, &current, history, nil
}

func strPtr(value string) *string { return &value }
