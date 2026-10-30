package folio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type Service struct {
	Store *store.Store
}

type View struct {
	Folio db.Folio               `json:"folio"`
	Lines []db.ListFolioLinesRow `json:"lines"`
}

type SplitRequest struct {
	TargetFolioID *int64
	PayerType     string
	PayerName     string
	TaxNumber     *string
	LineIDs       []int64
	AmountKurus   int64
	Percent       int
}

func (s *Service) List(ctx context.Context, reservationID int64) ([]View, error) {
	folios, err := s.Store.ListFolios(ctx, reservationID)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(folios))
	for _, f := range folios {
		lines, err := s.Store.ListFolioLines(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, View{Folio: f, Lines: lines})
	}
	return views, nil
}

func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	f, err := s.Store.GetFolio(ctx, id)
	if err != nil {
		return View{}, missing(err)
	}
	lines, err := s.Store.ListFolioLines(ctx, id)
	if err != nil {
		return View{}, err
	}
	return View{Folio: f, Lines: lines}, nil
}

func (s *Service) Charge(ctx context.Context, actor auditlog.Actor, id, amount int64, description string) (db.FolioLine, error) {
	if amount == 0 {
		return db.FolioLine{}, apperr.Invalid("Tutar sıfır olamaz")
	}
	if strings.TrimSpace(description) == "" {
		return db.FolioLine{}, apperr.Invalid("Açıklama zorunlu")
	}
	return s.post(ctx, actor, id, "folio.charge", db.InsertFolioLineParams{
		Kind: db.FolioLineKindExtra, AmountKurus: amount, Description: description,
	})
}

func (s *Service) Pay(ctx context.Context, actor auditlog.Actor, id, amount int64, method string) (db.FolioLine, error) {
	if amount <= 0 {
		return db.FolioLine{}, apperr.Invalid("Ödeme tutarı sıfırdan büyük olmalı")
	}
	if method != "nakit" && method != "kart" && method != "havale" {
		return db.FolioLine{}, apperr.Invalid("Ödeme yöntemi nakit, kart veya havale olmalı")
	}
	return s.post(ctx, actor, id, "folio.payment", db.InsertFolioLineParams{
		Kind: db.FolioLineKindPayment, AmountKurus: -amount, Description: "Ödeme (" + method + ")", Method: &method,
	})
}

func (s *Service) Refund(ctx context.Context, actor auditlog.Actor, id, paymentLineID, amount int64) (db.FolioLine, error) {
	if amount <= 0 {
		return db.FolioLine{}, apperr.Invalid("İade tutarı sıfırdan büyük olmalı")
	}
	var line db.FolioLine
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockFolio(ctx, id); err != nil {
			return missing(err)
		}
		payment, err := q.GetFolioLine(ctx, paymentLineID)
		if err != nil {
			return apperr.NotFound("Ödeme satırı bulunamadı")
		}
		if payment.FolioID != id || payment.Kind != db.FolioLineKindPayment {
			return apperr.Invalid("İade yalnızca bu folyodaki ödeme satırına yapılabilir")
		}
		refunded, err := q.RefundedAmount(ctx, &paymentLineID)
		if err != nil {
			return err
		}
		if refunded+amount > -payment.AmountKurus {
			return apperr.Conflict("REFUND_EXCEEDS_PAYMENT", "İade tutarı ödenen tutarı aşamaz")
		}
		line, err = insert(ctx, q, actor, id, "folio.refund", db.InsertFolioLineParams{
			Kind: db.FolioLineKindRefund, AmountKurus: amount, Description: fmt.Sprintf("İade (ödeme #%d)", paymentLineID),
			Method: payment.Method, RefundOfLineID: &paymentLineID,
		})
		return err
	})
	return line, err
}

func (s *Service) Split(ctx context.Context, actor auditlog.Actor, id int64, req SplitRequest) (View, error) {
	var target int64
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		source, err := q.GetFolio(ctx, id)
		if err != nil {
			return missing(err)
		}
		if req.TargetFolioID != nil {
			target = *req.TargetFolioID
		} else {
			created, err := s.newFolio(ctx, q, source, req)
			if err != nil {
				return err
			}
			target = created.ID
		}
		if target == id {
			return apperr.Invalid("Hedef folyo kaynak ile aynı olamaz")
		}
		locked, err := q.LockFoliosOrdered(ctx, []int64{id, target})
		if err != nil {
			return err
		}
		if len(locked) != 2 {
			return apperr.NotFound("Folyo bulunamadı")
		}
		for _, f := range locked {
			if f.ReservationID != source.ReservationID {
				return apperr.Invalid("Folyolar aynı rezervasyona ait olmalı")
			}
			if f.ClosedAt != nil {
				return apperr.Conflict("FOLIO_CLOSED", "Kapalı folyo değiştirilemez")
			}
			if f.ID == id {
				source = f
			}
		}
		state, err := q.GetHotelState(ctx)
		if err != nil {
			return err
		}
		group := uuid.New()
		if len(req.LineIDs) > 0 {
			return moveLines(ctx, q, actor, source, target, req.LineIDs, group, state.BusinessDate)
		}
		amount, err := splitAmount(source, req)
		if err != nil {
			return err
		}
		return transfer(ctx, q, actor, source, target, amount, nil, "Folyo bölme", group, state.BusinessDate)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, target)
}

func (s *Service) newFolio(ctx context.Context, q *db.Queries, source db.Folio, req SplitRequest) (db.Folio, error) {
	if req.PayerType != "guest" && req.PayerType != "company" {
		return db.Folio{}, apperr.Invalid("Ödeyici tipi guest veya company olmalı")
	}
	if strings.TrimSpace(req.PayerName) == "" {
		return db.Folio{}, apperr.Invalid("Ödeyici adı zorunlu")
	}
	return q.CreateFolio(ctx, db.CreateFolioParams{
		ReservationID: source.ReservationID, PayerType: req.PayerType, PayerName: strings.TrimSpace(req.PayerName), TaxNumber: req.TaxNumber,
	})
}

func splitAmount(source db.Folio, req SplitRequest) (int64, error) {
	amount := req.AmountKurus
	if req.Percent != 0 {
		if req.Percent < 1 || req.Percent > 100 {
			return 0, apperr.Invalid("Yüzde 1 ile 100 arasında olmalı")
		}
		amount = (source.BalanceKurus*int64(req.Percent) + 50) / 100
	}
	if amount <= 0 {
		return 0, apperr.Invalid("Bölünecek tutar sıfırdan büyük olmalı")
	}
	if amount > source.BalanceKurus {
		return 0, apperr.Conflict("SPLIT_EXCEEDS_BALANCE", "Bölünecek tutar folyo bakiyesini aşamaz")
	}
	return amount, nil
}

func moveLines(ctx context.Context, q *db.Queries, actor auditlog.Actor, source db.Folio, target int64, ids []int64, group uuid.UUID, day time.Time) error {
	for _, lineID := range ids {
		line, err := q.GetFolioLine(ctx, lineID)
		if err != nil {
			return apperr.NotFound("Folyo satırı bulunamadı")
		}
		if line.FolioID != source.ID {
			return apperr.Invalid("Satır bu folyoya ait değil")
		}
		if line.Kind == db.FolioLineKindPayment || line.Kind == db.FolioLineKindRefund || line.Kind == db.FolioLineKindTransfer {
			return apperr.Invalid("Yalnızca harcama satırları taşınabilir")
		}
		lineRef := lineID
		if err := transfer(ctx, q, actor, source, target, line.AmountKurus, &lineRef, line.Description, group, day); err != nil {
			if store.IsUnique(err) {
				return apperr.Conflict("LINE_ALREADY_MOVED", "Bu satır daha önce taşınmış")
			}
			return err
		}
	}
	return nil
}

func transfer(ctx context.Context, q *db.Queries, actor auditlog.Actor, source db.Folio, target, amount int64, lineID *int64, label string, group uuid.UUID, day time.Time) error {
	base := db.InsertFolioLineParams{
		ReservationID: source.ReservationID, Kind: db.FolioLineKindTransfer, BusinessDate: day,
		TransferGroup: &group, MovedLineID: lineID, PostedBy: actor.ID,
	}
	out := base
	out.FolioID, out.AmountKurus, out.Description = source.ID, -amount, "Aktarım: "+label
	if _, err := q.InsertFolioLine(ctx, out); err != nil {
		return err
	}
	in := base
	in.FolioID, in.AmountKurus, in.Description = target, amount, "Aktarım: "+label
	if _, err := q.InsertFolioLine(ctx, in); err != nil {
		return err
	}
	return auditlog.Write(ctx, q, actor, "folio.split", "folio", source.ID, nil, map[string]any{"target": target, "amount_kurus": amount})
}

func (s *Service) post(ctx context.Context, actor auditlog.Actor, id int64, action string, line db.InsertFolioLineParams) (db.FolioLine, error) {
	var out db.FolioLine
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockFolio(ctx, id); err != nil {
			return missing(err)
		}
		var err error
		out, err = insert(ctx, q, actor, id, action, line)
		return err
	})
	return out, err
}

func insert(ctx context.Context, q *db.Queries, actor auditlog.Actor, id int64, action string, line db.InsertFolioLineParams) (db.FolioLine, error) {
	folio, err := q.GetFolio(ctx, id)
	if err != nil {
		return db.FolioLine{}, missing(err)
	}
	if folio.ClosedAt != nil {
		return db.FolioLine{}, apperr.Conflict("FOLIO_CLOSED", "Kapalı folyoya işlem yapılamaz")
	}
	state, err := q.GetHotelState(ctx)
	if err != nil {
		return db.FolioLine{}, err
	}
	line.FolioID, line.ReservationID, line.BusinessDate, line.PostedBy = id, folio.ReservationID, state.BusinessDate, actor.ID
	saved, err := q.InsertFolioLine(ctx, line)
	if err != nil {
		return db.FolioLine{}, err
	}
	return saved, auditlog.Write(ctx, q, actor, action, "folio", id, map[string]int64{"balance_kurus": folio.BalanceKurus},
		map[string]int64{"balance_kurus": folio.BalanceKurus + line.AmountKurus})
}

func missing(err error) error {
	if store.IsNotFound(err) {
		return apperr.NotFound("Folyo bulunamadı")
	}
	return err
}
