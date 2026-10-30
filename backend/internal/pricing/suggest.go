package pricing

type Suggestion struct {
	Date         string `json:"date"`
	RoomType     string `json:"room_type"`
	OccupancyPct int    `json:"occupancy_pct"`
	ChangeBP     int    `json:"change_bp"`
	Reason       string `json:"reason"`
}

func Suggest(date, roomType string, sold, capacity int) Suggestion {
	s := Suggestion{Date: date, RoomType: roomType}
	if capacity > 0 {
		s.OccupancyPct = sold * 100 / capacity
	}
	switch {
	case s.OccupancyPct >= 90:
		s.ChangeBP, s.Reason = 1500, "Doluluk çok yüksek, fiyat artırılabilir"
	case s.OccupancyPct >= 75:
		s.ChangeBP, s.Reason = 500, "Talep güçlü, küçük artış önerilir"
	case s.OccupancyPct < 40:
		s.ChangeBP, s.Reason = -1000, "Doluluk düşük, indirim önerilir"
	default:
		s.Reason = "Fiyat korunabilir"
	}
	return s
}
