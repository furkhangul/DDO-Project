package seed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"konakpro/internal/allocation"
	"konakpro/internal/auth"
)

const (
	DemoPassword = "Konak!2026"
	historyYears = 3
	forwardDays  = 120
	inventoryEnd = 400
	solverDays   = 30
)

type RoomType struct {
	ID            int64
	Code, Name    string
	Description   string
	Rooms         int
	BaseOccupancy int
	MaxOccupancy  int
	OverbookPct   int
	BaseKurus     int64
	ExtraKurus    int64
	ChildKurus    int64
}

type Room struct {
	ID     int64
	Number string
	TypeID int64
	Floor  int
	View   string
	Status string
}

type User struct {
	ID       int64
	Email    string
	Name     string
	Role     string
	Password string `json:"-"`
	Hash     string `json:"-"`
}

type Guest struct {
	ID     int64
	Name   string
	Email  string
	UserID *int64
}

type Reservation struct {
	ID            int64
	Code          string
	GuestID       int64
	TypeID        int64
	PlanID        int64
	CheckIn       time.Time
	CheckOut      time.Time
	Adults        int
	Status        string
	ChannelID     *int64
	ExternalRef   *string
	PrefFloor     *int
	PrefView      *string
	AdjacentGroup *string
	BookedOn      time.Time
	NetKurus      int64
	TaxKurus      int64
}

type Night struct {
	ReservationID int64
	Date          time.Time
	Net, KDV      int64
	Accom         int64
}

type Assignment struct {
	ReservationID int64
	RoomID        int64
	From, To      time.Time
	Locked        bool
}

type Inventory struct {
	TypeID   int64
	Date     time.Time
	Capacity int
	Limit    int
	Sold     int
}

type Folio struct {
	ID            int64
	ReservationID int64
	Payer         string
}

type FolioLine struct {
	FolioID       int64
	ReservationID int64
	Kind          string
	Amount        int64
	Description   string
	Date          time.Time
	Method        *string
}

type Daily struct {
	Date    time.Time
	Rooms   int
	Sold    int
	Revenue int64
}

type Task struct {
	RoomID int64
	Type   string
	Date   time.Time
}

type Dataset struct {
	Seed         int64
	Anchor       time.Time
	Types        []RoomType
	Rooms        []Room
	Adjacent     [][2]int64
	Users        []User
	Guests       []Guest
	Reservations []Reservation
	Nights       []Night
	Assignments  []Assignment
	Inventory    []Inventory
	Folios       []Folio
	Lines        []FolioLine
	Daily        []Daily
	Tasks        []Task
	Digest       string
}

var catalog = []RoomType{
	{Code: "STD", Name: "Standart", Description: "Avlu veya şehir manzaralı konforlu oda", Rooms: 60, BaseOccupancy: 2, MaxOccupancy: 3, OverbookPct: 5, BaseKurus: 320000, ExtraKurus: 90000, ChildKurus: 45000},
	{Code: "DLX", Name: "Deluxe", Description: "Geniş oturma alanlı, balkonlu oda", Rooms: 36, BaseOccupancy: 2, MaxOccupancy: 3, OverbookPct: 5, BaseKurus: 480000, ExtraKurus: 120000, ChildKurus: 60000},
	{Code: "FAM", Name: "Aile", Description: "Bağlantılı iki bölümlü aile odası", Rooms: 18, BaseOccupancy: 3, MaxOccupancy: 5, OverbookPct: 0, BaseKurus: 650000, ExtraKurus: 110000, ChildKurus: 55000},
	{Code: "SUI", Name: "Süit", Description: "Deniz manzaralı konak süiti", Rooms: 6, BaseOccupancy: 2, MaxOccupancy: 4, OverbookPct: 0, BaseKurus: 1150000, ExtraKurus: 200000, ChildKurus: 100000},
}

var (
	firstNames = []string{"Ayşe", "Mehmet", "Zeynep", "Mustafa", "Elif", "Ahmet", "Defne", "Emre", "Selin", "Can", "Deniz", "Burak", "Ece", "Kerem", "Nazlı", "Oğuz", "Pelin", "Tolga", "Sibel", "Umut", "Anna", "Lukas", "Sophie", "Marco", "Yuki", "Omar"}
	lastNames  = []string{"Yılmaz", "Kaya", "Demir", "Çelik", "Şahin", "Öztürk", "Aydın", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Koç", "Kurt", "Özdemir", "Müller", "Rossi", "Tanaka", "Haddad", "Smith"}
	views      = []string{"deniz", "bahce", "sehir"}
)

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func Build(seedValue int64, anchor time.Time) (*Dataset, error) {
	anchor = dateOnly(anchor)
	rng := rand.New(rand.NewSource(seedValue))
	d := &Dataset{Seed: seedValue, Anchor: anchor}
	d.buildRooms()
	if err := d.buildUsers(rng); err != nil {
		return nil, err
	}
	d.buildStays(rng)
	d.buildDemoStay()
	d.buildAssignments(seedValue)
	d.buildInventory()
	d.buildFolios(rng)
	d.buildDaily()
	d.buildTasks()
	d.Digest = d.digest()
	return d, nil
}

func (d *Dataset) buildRooms() {
	id := int64(1)
	for i := range catalog {
		t := catalog[i]
		t.ID = int64(i + 1)
		d.Types = append(d.Types, t)
	}
	for _, t := range d.Types {
		for n := 0; n < t.Rooms; n++ {
			floor := n/20 + 1
			if t.Code == "SUI" {
				floor = 6
			}
			view := views[n%3]
			if t.Code == "SUI" {
				view = "deniz"
			}
			d.Rooms = append(d.Rooms, Room{ID: id, TypeID: t.ID, Floor: floor, View: view, Status: "clean"})
			id++
		}
	}
	perFloor := map[int]int{}
	for i := range d.Rooms {
		perFloor[d.Rooms[i].Floor]++
		d.Rooms[i].Number = fmt.Sprintf("%d%02d", d.Rooms[i].Floor, perFloor[d.Rooms[i].Floor])
	}
	for i := 1; i < len(d.Rooms); i++ {
		if d.Rooms[i].Floor == d.Rooms[i-1].Floor {
			d.Adjacent = append(d.Adjacent, [2]int64{d.Rooms[i-1].ID, d.Rooms[i].ID})
		}
	}
}

func (d *Dataset) buildUsers(rng *rand.Rand) error {
	staff := []User{
		{Email: "resepsiyon@konakpro.test", Name: "Leyla Aksoy", Role: "reception"},
		{Email: "rezervasyon@konakpro.test", Name: "Barış Tekin", Role: "reservations"},
		{Email: "kathizmeti@konakpro.test", Name: "Gül Erdem", Role: "housekeeping"},
		{Email: "gelir@konakpro.test", Name: "Hakan Yıldız", Role: "revenue"},
		{Email: "mudur@konakpro.test", Name: "Nilüfer Sezer", Role: "manager"},
		{Email: "misafir@konakpro.test", Name: "Ali Vural", Role: "guest"},
	}
	for i := range staff {
		salt := make([]byte, 16)
		rng.Read(salt)
		staff[i].ID = int64(i + 1)
		staff[i].Password = DemoPassword
		staff[i].Hash = auth.HashWithSalt(DemoPassword, salt)
	}
	d.Users = staff
	return nil
}

func seasonBP(day time.Time) int64 {
	switch day.Month() {
	case time.June, time.July, time.August:
		return 15000
	case time.September, time.April, time.May:
		return 11500
	case time.December:
		return 10500
	}
	return 9000
}

func targetOccupancy(day time.Time) float64 {
	switch day.Month() {
	case time.July, time.August:
		return 0.93
	case time.June, time.September:
		return 0.82
	case time.April, time.May, time.October:
		return 0.68
	case time.December:
		return 0.6
	}
	return 0.46
}

func nightNet(t RoomType, day time.Time) int64 {
	amount := t.BaseKurus * seasonBP(day) / 10000
	if wd := day.Weekday(); wd == time.Friday || wd == time.Saturday {
		amount = amount * 12000 / 10000
	}
	return amount
}

type counter struct {
	sold map[int64]map[time.Time]int
}

func (c *counter) get(typeID int64, day time.Time) int { return c.sold[typeID][day] }

func (c *counter) add(typeID int64, day time.Time) {
	if c.sold[typeID] == nil {
		c.sold[typeID] = map[time.Time]int{}
	}
	c.sold[typeID][day]++
}

func (d *Dataset) buildStays(rng *rand.Rand) {
	start := d.Anchor.AddDate(-historyYears, 0, 0)
	end := d.Anchor.AddDate(0, 0, forwardDays)
	sold := &counter{sold: map[int64]map[time.Time]int{}}
	guestID := int64(1)
	d.Guests = append(d.Guests, Guest{ID: guestID, Name: "Ali Vural", Email: "misafir@konakpro.test", UserID: &d.Users[5].ID})
	channelIDs := []int64{1, 2, 3}

	resID := int64(0)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		target := targetOccupancy(day)
		ahead := int(day.Sub(d.Anchor).Hours() / 24)
		if ahead > 0 {
			target *= 1 - 0.75*float64(ahead)/float64(forwardDays)
		}
		for _, t := range d.Types {
			wanted := int(float64(t.Rooms)*target) - sold.get(t.ID, day)
			for wanted > 0 {
				length := 1 + rng.Intn(6)
				out := day.AddDate(0, 0, length)
				if !fits(sold, t, day, out) {
					out = day.AddDate(0, 0, 1)
				}
				cancelled := rng.Intn(100) < 7
				wanted--
				guestID++
				name := firstNames[rng.Intn(len(firstNames))] + " " + lastNames[rng.Intn(len(lastNames))]
				d.Guests = append(d.Guests, Guest{ID: guestID, Name: name})
				resID++
				lead := rng.Intn(70)
				booked := day.AddDate(0, 0, -lead)
				if booked.After(d.Anchor) {
					booked = d.Anchor
				}
				r := Reservation{
					ID: resID, Code: fmt.Sprintf("KP%07d", resID), GuestID: guestID, TypeID: t.ID,
					PlanID: int64(1 + rng.Intn(2)), CheckIn: day, CheckOut: out, Adults: 1 + rng.Intn(t.BaseOccupancy), BookedOn: booked,
				}
				if pick := rng.Intn(10); pick < 5 {
					channel := channelIDs[pick%3]
					ref := fmt.Sprintf("H-%d-%06d", channel, resID)
					r.ChannelID, r.ExternalRef = &channel, &ref
				}
				if rng.Intn(5) == 0 {
					view := views[rng.Intn(3)]
					r.PrefView = &view
				}
				if rng.Intn(8) == 0 {
					floor := 1 + rng.Intn(5)
					r.PrefFloor = &floor
				}
				r.Status = d.statusOf(day, out, cancelled)
				if !cancelled {
					for n := day; n.Before(out); n = n.AddDate(0, 0, 1) {
						sold.add(t.ID, n)
						net := nightNet(t, n)
						kdv, accom := net/10, net/50
						d.Nights = append(d.Nights, Night{ReservationID: r.ID, Date: n, Net: net, KDV: kdv, Accom: accom})
						r.NetKurus += net
						r.TaxKurus += kdv + accom
					}
				}
				d.Reservations = append(d.Reservations, r)
			}
		}
	}
}

func fits(sold *counter, t RoomType, from, to time.Time) bool {
	for n := from; n.Before(to); n = n.AddDate(0, 0, 1) {
		if sold.get(t.ID, n) >= t.Rooms {
			return false
		}
	}
	return true
}

func (d *Dataset) statusOf(in, out time.Time, cancelled bool) string {
	switch {
	case cancelled:
		return "cancelled"
	case !out.After(d.Anchor) && out.Before(d.Anchor):
		return "checked_out"
	case in.Before(d.Anchor):
		return "checked_in"
	}
	return "confirmed"
}

func (d *Dataset) buildDemoStay() {
	t := d.Types[1]
	id := int64(len(d.Reservations) + 1)
	out := d.Anchor.AddDate(0, 0, 2)
	r := Reservation{
		ID: id, Code: "KPDEMO001", GuestID: 1, TypeID: t.ID, PlanID: 1, CheckIn: d.Anchor, CheckOut: out,
		Adults: 2, Status: "confirmed", BookedOn: d.Anchor.AddDate(0, 0, -10),
	}
	for n := d.Anchor; n.Before(out); n = n.AddDate(0, 0, 1) {
		net := nightNet(t, n)
		kdv, accom := net/10, net/50
		d.Nights = append(d.Nights, Night{ReservationID: id, Date: n, Net: net, KDV: kdv, Accom: accom})
		r.NetKurus += net
		r.TaxKurus += kdv + accom
	}
	d.Reservations = append(d.Reservations, r)
}

func (d *Dataset) buildAssignments(seedValue int64) {
	horizon := d.Anchor.AddDate(0, 0, solverDays)
	origin := d.Anchor.AddDate(0, 0, -14)
	input := allocation.Input{Seed: seedValue, Iterations: 30000, Budget: 30 * time.Second, Adjacent: d.Adjacent}
	for _, room := range d.Rooms {
		input.Rooms = append(input.Rooms, allocation.Room{ID: room.ID, TypeID: room.TypeID, Floor: room.Floor, View: room.View})
	}
	byID := map[int64]*Reservation{}
	for i := range d.Reservations {
		r := &d.Reservations[i]
		if r.Status != "confirmed" && r.Status != "checked_in" {
			continue
		}
		if !r.CheckIn.Before(horizon) {
			continue
		}
		byID[r.ID] = r
		stay := allocation.Stay{
			ID: r.ID, TypeID: r.TypeID,
			From: int(r.CheckIn.Sub(origin).Hours() / 24), To: int(r.CheckOut.Sub(origin).Hours() / 24),
		}
		if r.PrefFloor != nil {
			stay.PrefFloor = r.PrefFloor
		}
		if r.PrefView != nil {
			stay.PrefView = *r.PrefView
		}
		input.Stays = append(input.Stays, stay)
	}
	result := allocation.Solve(input)
	for _, seg := range result.Segments {
		r := byID[seg.StayID]
		d.Assignments = append(d.Assignments, Assignment{
			ReservationID: seg.StayID, RoomID: seg.RoomID,
			From: origin.AddDate(0, 0, seg.From), To: origin.AddDate(0, 0, seg.To), Locked: r.Status == "checked_in",
		})
	}
	sort.Slice(d.Assignments, func(i, j int) bool {
		a, b := d.Assignments[i], d.Assignments[j]
		if a.ReservationID != b.ReservationID {
			return a.ReservationID < b.ReservationID
		}
		return a.From.Before(b.From)
	})
}

func (d *Dataset) buildInventory() {
	sold := map[int64]map[time.Time]int{}
	statuses := map[int64]Reservation{}
	for _, r := range d.Reservations {
		statuses[r.ID] = r
	}
	for _, n := range d.Nights {
		r := statuses[n.ReservationID]
		if r.Status != "confirmed" && r.Status != "checked_in" {
			continue
		}
		if sold[r.TypeID] == nil {
			sold[r.TypeID] = map[time.Time]int{}
		}
		sold[r.TypeID][n.Date]++
	}
	for _, t := range d.Types {
		for i := 0; i < inventoryEnd; i++ {
			day := d.Anchor.AddDate(0, 0, i)
			d.Inventory = append(d.Inventory, Inventory{
				TypeID: t.ID, Date: day, Capacity: t.Rooms, Limit: t.Rooms * t.OverbookPct / 100, Sold: sold[t.ID][day],
			})
		}
	}
}

func (d *Dataset) buildFolios(rng *rand.Rand) {
	id := int64(0)
	nights := map[int64][]Night{}
	for _, n := range d.Nights {
		nights[n.ReservationID] = append(nights[n.ReservationID], n)
	}
	names := map[int64]string{}
	for _, g := range d.Guests {
		names[g.ID] = g.Name
	}
	for _, r := range d.Reservations {
		if r.Status != "confirmed" && r.Status != "checked_in" {
			continue
		}
		id++
		d.Folios = append(d.Folios, Folio{ID: id, ReservationID: r.ID, Payer: names[r.GuestID]})
		if r.Status != "checked_in" {
			continue
		}
		var posted int64
		for _, n := range nights[r.ID] {
			if !n.Date.Before(d.Anchor) {
				break
			}
			stamp := n.Date.Format("02.01.2006")
			d.Lines = append(d.Lines,
				FolioLine{id, r.ID, "room", n.Net, "Oda ücreti " + stamp, n.Date, nil},
				FolioLine{id, r.ID, "tax_kdv", n.KDV, "KDV " + stamp, n.Date, nil},
				FolioLine{id, r.ID, "tax_accom", n.Accom, "Konaklama vergisi " + stamp, n.Date, nil},
			)
			posted += n.Net + n.KDV + n.Accom
		}
		if rng.Intn(3) == 0 {
			method := []string{"kart", "nakit", "havale"}[rng.Intn(3)]
			d.Lines = append(d.Lines, FolioLine{id, r.ID, "payment", -posted, "Ödeme (" + method + ")", d.Anchor.AddDate(0, 0, -1), &method})
		}
	}
}

func (d *Dataset) buildDaily() {
	status := map[int64]string{}
	for _, r := range d.Reservations {
		status[r.ID] = r.Status
	}
	sold := map[time.Time]int{}
	revenue := map[time.Time]int64{}
	for _, n := range d.Nights {
		if n.Date.Before(d.Anchor) && (status[n.ReservationID] == "checked_out" || status[n.ReservationID] == "checked_in") {
			sold[n.Date]++
			revenue[n.Date] += n.Net
		}
	}
	for day := d.Anchor.AddDate(-historyYears, 0, 0); day.Before(d.Anchor); day = day.AddDate(0, 0, 1) {
		d.Daily = append(d.Daily, Daily{Date: day, Rooms: len(d.Rooms), Sold: sold[day], Revenue: revenue[day]})
	}
}

func (d *Dataset) buildTasks() {
	seen := map[int64]bool{}
	for _, a := range d.Assignments {
		if a.Locked && a.From.Before(d.Anchor) && a.To.After(d.Anchor) && !seen[a.RoomID] {
			seen[a.RoomID] = true
			d.Tasks = append(d.Tasks, Task{RoomID: a.RoomID, Type: "konaklama_temizligi", Date: d.Anchor})
		}
	}
}

func (d *Dataset) digest() string {
	raw, err := json.Marshal(struct {
		Types        []RoomType
		Rooms        []Room
		Users        []User
		Guests       []Guest
		Reservations []Reservation
		Nights       []Night
		Assignments  []Assignment
		Inventory    []Inventory
		Lines        []FolioLine
	}{d.Types, d.Rooms, d.Users, d.Guests, d.Reservations, d.Nights, d.Assignments, d.Inventory, d.Lines})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
