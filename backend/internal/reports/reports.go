package reports

import (
	"context"
	"time"

	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type Service struct {
	Store *store.Store
}

type Point struct {
	Date           string  `json:"date"`
	RoomsAvailable int32   `json:"rooms_available"`
	RoomsSold      int32   `json:"rooms_sold"`
	RevenueKurus   int64   `json:"room_revenue_kurus"`
	OccupancyPct   float64 `json:"occupancy_pct"`
	ADRKurus       int64   `json:"adr_kurus"`
	RevPARKurus    int64   `json:"revpar_kurus"`
	OnTheBooks     bool    `json:"on_the_books"`
}

type KPIReport struct {
	Points  []Point                   `json:"points"`
	Summary Point                     `json:"summary"`
	ByType  []db.RevenueByRoomTypeRow `json:"by_type"`
}

func Derive(p *Point) {
	if p.RoomsAvailable > 0 {
		p.OccupancyPct = float64(p.RoomsSold*1000/p.RoomsAvailable) / 10
		p.RevPARKurus = p.RevenueKurus / int64(p.RoomsAvailable)
	}
	if p.RoomsSold > 0 {
		p.ADRKurus = p.RevenueKurus / int64(p.RoomsSold)
	}
}

func Aggregate(points []Point, label string) Point {
	sum := Point{Date: label}
	for _, p := range points {
		sum.RoomsAvailable += p.RoomsAvailable
		sum.RoomsSold += p.RoomsSold
		sum.RevenueKurus += p.RevenueKurus
	}
	Derive(&sum)
	return sum
}

func Rollup(points []Point) []Point {
	var out []Point
	var bucket []Point
	flush := func() {
		if len(bucket) > 0 {
			out = append(out, Aggregate(bucket, bucket[0].Date[:7]))
			bucket = nil
		}
	}
	for _, p := range points {
		if len(bucket) > 0 && bucket[0].Date[:7] != p.Date[:7] {
			flush()
		}
		bucket = append(bucket, p)
	}
	flush()
	return out
}

func (s *Service) KPI(ctx context.Context, from, to time.Time, monthly bool) (KPIReport, error) {
	if !to.After(from) {
		return KPIReport{}, apperr.Invalid("Bitiş tarihi başlangıçtan sonra olmalı")
	}
	if to.Sub(from) > 4*366*24*time.Hour {
		return KPIReport{}, apperr.Invalid("Rapor aralığı en fazla 4 yıl olabilir")
	}
	state, err := s.Store.GetHotelState(ctx)
	if err != nil {
		return KPIReport{}, err
	}
	rows, err := s.Store.KPISeries(ctx, db.KPISeriesParams{StayDate: from, StayDate_2: to})
	if err != nil {
		return KPIReport{}, err
	}
	points := make([]Point, 0, len(rows))
	for _, row := range rows {
		p := Point{Date: row.StayDate.Format(time.DateOnly), RoomsAvailable: row.RoomsAvailable, RoomsSold: row.RoomsSold, RevenueKurus: row.RoomRevenueKurus}
		Derive(&p)
		points = append(points, p)
	}
	future, err := s.Store.OnTheBooks(ctx, db.OnTheBooksParams{StayDate: maxDate(from, state.BusinessDate), StayDate_2: to})
	if err != nil {
		return KPIReport{}, err
	}
	counts, err := s.Store.DashboardCounts(ctx, state.BusinessDate)
	if err != nil {
		return KPIReport{}, err
	}
	for _, row := range future {
		p := Point{
			Date: row.StayDate.Format(time.DateOnly), RoomsAvailable: counts.RoomsTotal - counts.RoomsOutOfOrder,
			RoomsSold: row.RoomsSold, RevenueKurus: row.RoomRevenueKurus, OnTheBooks: true,
		}
		Derive(&p)
		points = append(points, p)
	}
	report := KPIReport{Summary: Aggregate(points, "toplam")}
	report.Points = points
	if monthly {
		report.Points = Rollup(points)
	}
	report.ByType, err = s.Store.RevenueByRoomType(ctx, db.RevenueByRoomTypeParams{StayDate: from, StayDate_2: to})
	return report, err
}

func maxDate(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
