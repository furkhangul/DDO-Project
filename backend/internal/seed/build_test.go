package seed

import (
	"testing"
	"time"
)

var anchor = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func TestSameSeedSameDataset(t *testing.T) {
	a, err := Build(42, anchor)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(42, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest != b.Digest {
		t.Fatalf("aynı tohum farklı veri üretti: %s != %s", a.Digest, b.Digest)
	}
	c, err := Build(43, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == c.Digest {
		t.Fatal("farklı tohum aynı veriyi üretti")
	}
}

func TestDatasetShape(t *testing.T) {
	d, err := Build(42, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rooms) != 120 {
		t.Fatalf("oda sayısı %d", len(d.Rooms))
	}
	if first := d.Reservations[0].CheckIn; first.After(anchor.AddDate(-3, 0, 1)) {
		t.Fatalf("üç yıllık geçmiş yok: %v", first)
	}
	roles := map[string]bool{}
	for _, u := range d.Users {
		roles[u.Role] = true
	}
	if len(roles) != 6 {
		t.Fatalf("rol sayısı %d", len(roles))
	}
	inHouse, arrivals := 0, 0
	for _, r := range d.Reservations {
		if r.Status == "checked_in" {
			inHouse++
		}
		if r.Status == "confirmed" && r.CheckIn.Equal(anchor) {
			arrivals++
		}
	}
	if inHouse == 0 || arrivals == 0 {
		t.Fatalf("konaklayan=%d gelen=%d", inHouse, arrivals)
	}
	t.Logf("rezervasyon=%d gece=%d konaklayan=%d bugün gelen=%d", len(d.Reservations), len(d.Nights), inHouse, arrivals)
}

func TestInventoryNeverExceedsCapacity(t *testing.T) {
	d, err := Build(7, anchor)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range d.Inventory {
		if row.Sold > row.Capacity {
			t.Fatalf("seed fazla satış üretti: %+v", row)
		}
	}
}

func TestInHouseGuestsHaveRooms(t *testing.T) {
	d, err := Build(9, anchor)
	if err != nil {
		t.Fatal(err)
	}
	assigned := map[int64]bool{}
	for _, a := range d.Assignments {
		assigned[a.ReservationID] = true
	}
	for _, r := range d.Reservations {
		if r.Status == "checked_in" && !assigned[r.ID] {
			t.Fatalf("konaklayan misafirin odası yok: %s", r.Code)
		}
	}
}
