package allocation

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func hotel() ([]Room, [][2]int64) {
	views := []string{"deniz", "bahce", "sehir"}
	var rooms []Room
	var adjacent [][2]int64
	for i := 0; i < 120; i++ {
		id := int64(i + 1)
		typeID := int64(i%4 + 1)
		if i < 60 {
			typeID = 1
		}
		rooms = append(rooms, Room{ID: id, TypeID: typeID, Floor: i/20 + 1, View: views[i%3]})
		if i%20 != 19 {
			adjacent = append(adjacent, [2]int64{id, id + 1})
		}
	}
	return rooms, adjacent
}

func workload(rooms []Room, n, horizon int, seed int64) []Stay {
	rng := rand.New(rand.NewSource(seed))
	counts := map[int64]int{}
	for _, r := range rooms {
		counts[r.TypeID]++
	}
	load := map[int64][]int{}
	for t := range counts {
		load[t] = make([]int, horizon+15)
	}
	types := []int64{1, 1, 1, 2, 3, 4}
	views := []string{"", "", "deniz", "bahce"}
	var stays []Stay
	for id := int64(1); len(stays) < n; id++ {
		from := rng.Intn(horizon)
		length := 1 + rng.Intn(6)
		typeID := types[rng.Intn(len(types))]
		fits := true
		for d := from; d < from+length; d++ {
			if load[typeID][d] >= counts[typeID] {
				fits = false
				break
			}
		}
		if !fits {
			continue
		}
		for d := from; d < from+length; d++ {
			load[typeID][d]++
		}
		st := Stay{ID: id, TypeID: typeID, From: from, To: from + length, PrefView: views[rng.Intn(len(views))]}
		if rng.Intn(4) == 0 {
			f := 1 + rng.Intn(6)
			st.PrefFloor = &f
		}
		stays = append(stays, st)
	}
	return stays
}

func assertNoDoubleBooking(t *testing.T, in Input, r Result) {
	t.Helper()
	used := map[[2]int64]int64{}
	roomType := map[int64]int64{}
	for _, room := range in.Rooms {
		roomType[room.ID] = room.TypeID
	}
	stayType := map[int64]Stay{}
	for _, st := range in.Stays {
		stayType[st.ID] = st
	}
	covered := map[int64]int{}
	for _, seg := range r.Segments {
		st := stayType[seg.StayID]
		if roomType[seg.RoomID] != st.TypeID {
			t.Fatalf("oda tipi uyuşmuyor: %+v", seg)
		}
		for d := seg.From; d < seg.To; d++ {
			key := [2]int64{seg.RoomID, int64(d)}
			if other, ok := used[key]; ok {
				t.Fatalf("oda %d gün %d iki kez verildi: %d ve %d", seg.RoomID, d, other, seg.StayID)
			}
			used[key] = seg.StayID
			covered[seg.StayID]++
		}
	}
	for _, id := range r.Unassigned {
		if covered[id] != 0 {
			t.Fatalf("atanamayan konaklama kısmen yerleşti: %d", id)
		}
	}
	for id, nights := range covered {
		if st := stayType[id]; nights != st.To-st.From {
			t.Fatalf("konaklama %d eksik: %d/%d", id, nights, st.To-st.From)
		}
	}
}

func TestNoPinsMeansZeroMovesWhenCapacityFits(t *testing.T) {
	rooms, adjacent := hotel()
	in := Input{Rooms: rooms, Adjacent: adjacent, Stays: workload(rooms, 400, 20, 7), Seed: 1}
	r := Solve(in)
	assertNoDoubleBooking(t, in, r)
	if r.Moves != 0 || len(r.Unassigned) != 0 {
		t.Fatalf("taşıma=%d atanamayan=%d", r.Moves, len(r.Unassigned))
	}
}

func TestSolvesFullScenarioWithinTenSeconds(t *testing.T) {
	rooms, adjacent := hotel()
	in := Input{Rooms: rooms, Adjacent: adjacent, Stays: workload(rooms, 400, 14, 11), Seed: 3, Budget: 8 * time.Second}
	started := time.Now()
	r := Solve(in)
	elapsed := time.Since(started)
	assertNoDoubleBooking(t, in, r)
	if elapsed > 10*time.Second {
		t.Fatalf("süre aşıldı: %v", elapsed)
	}
	t.Logf("süre=%v taşıma=%d tercih=%d/%d maliyet %d -> %d", elapsed, r.Moves, r.PrefsMet, r.PrefsTotal, r.InitialCost, r.FinalCost)
}

func TestLocalSearchImprovesPreferences(t *testing.T) {
	rooms, adjacent := hotel()
	in := Input{Rooms: rooms, Adjacent: adjacent, Stays: workload(rooms, 400, 20, 5), Seed: 9}
	r := Solve(in)
	if r.FinalCost > r.InitialCost {
		t.Fatalf("yerel arama kötüleştirdi: %d -> %d", r.InitialCost, r.FinalCost)
	}
	base := FirstFit(in)
	if r.PrefsMet <= base.PrefsMet {
		t.Fatalf("çözücü taban çizgisini geçemedi: %d <= %d", r.PrefsMet, base.PrefsMet)
	}
}

func TestSameSeedSameResult(t *testing.T) {
	rooms, adjacent := hotel()
	in := Input{Rooms: rooms, Adjacent: adjacent, Stays: workload(rooms, 200, 20, 4), Seed: 77, Iterations: 20000, Budget: time.Minute}
	a, b := Solve(in), Solve(in)
	if !reflect.DeepEqual(a.Segments, b.Segments) {
		t.Fatal("aynı tohum farklı sonuç verdi")
	}
}

func TestPinnedStayStaysInPlace(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}, {ID: 2, TypeID: 1}}
	in := Input{Rooms: rooms, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 3, PinRoom: 2},
		{ID: 2, TypeID: 1, From: 0, To: 3},
	}}
	r := Solve(in)
	for _, seg := range r.Segments {
		if seg.StayID == 1 && seg.RoomID != 2 {
			t.Fatalf("pinlenmiş konaklama taşındı: %+v", seg)
		}
	}
	assertNoDoubleBooking(t, in, r)
}

func TestSplitOnlyWhenPinsForceIt(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}, {ID: 2, TypeID: 1}}
	in := Input{Rooms: rooms, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 2, PinRoom: 1},
		{ID: 2, TypeID: 1, From: 2, To: 4, PinRoom: 2},
		{ID: 3, TypeID: 1, From: 0, To: 4},
	}}
	r := Solve(in)
	assertNoDoubleBooking(t, in, r)
	if r.Moves != 1 || len(r.Unassigned) != 0 {
		t.Fatalf("tek taşıma bekleniyordu: %+v", r)
	}
}

func TestEjectionAvoidsMove(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}, {ID: 2, TypeID: 1}}
	in := Input{Rooms: rooms, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 2},
		{ID: 2, TypeID: 1, From: 2, To: 3, PinRoom: 1},
		{ID: 3, TypeID: 1, From: 3, To: 5},
		{ID: 4, TypeID: 1, From: 1, To: 4},
	}}
	r := Solve(in)
	assertNoDoubleBooking(t, in, r)
	if r.Moves != 0 {
		t.Fatalf("taşıma olmamalıydı: %+v", r)
	}
}

func TestOverbookedStayIsReportedUnassigned(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}}
	in := Input{Rooms: rooms, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 3},
		{ID: 2, TypeID: 1, From: 1, To: 2},
	}}
	r := Solve(in)
	assertNoDoubleBooking(t, in, r)
	if len(r.Unassigned) != 1 || r.Unassigned[0] != 2 {
		t.Fatalf("atanamayan: %+v", r)
	}
}

func TestAdjacentGroupPlacedNextToEachOther(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}, {ID: 2, TypeID: 1}, {ID: 3, TypeID: 1}, {ID: 4, TypeID: 1}}
	in := Input{Rooms: rooms, Adjacent: [][2]int64{{3, 4}}, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 3, Group: "aile"},
		{ID: 2, TypeID: 1, From: 0, To: 3, Group: "aile"},
	}, Seed: 2}
	r := Solve(in)
	rooms2 := map[int64]bool{}
	for _, seg := range r.Segments {
		rooms2[seg.RoomID] = true
	}
	if !rooms2[3] || !rooms2[4] {
		t.Fatalf("bitişik yerleşmedi: %+v", r.Segments)
	}
}

func TestViewAndFloorPreference(t *testing.T) {
	two := 2
	rooms := []Room{{ID: 1, TypeID: 1, Floor: 1, View: "bahce"}, {ID: 2, TypeID: 1, Floor: 2, View: "deniz"}}
	in := Input{Rooms: rooms, Stays: []Stay{{ID: 1, TypeID: 1, From: 0, To: 2, PrefFloor: &two, PrefView: "deniz"}}}
	r := Solve(in)
	if r.Segments[0].RoomID != 2 || r.PrefsMet != 2 {
		t.Fatalf("tercih karşılanmadı: %+v", r)
	}
}

func TestEmptyInput(t *testing.T) {
	r := Solve(Input{})
	if len(r.Segments) != 0 || r.Moves != 0 {
		t.Fatalf("boş girdi: %+v", r)
	}
}

func TestPinnedConflictIsUnassignedNotCrash(t *testing.T) {
	rooms := []Room{{ID: 1, TypeID: 1}}
	in := Input{Rooms: rooms, Stays: []Stay{
		{ID: 1, TypeID: 1, From: 0, To: 2, PinRoom: 1},
		{ID: 2, TypeID: 1, From: 1, To: 3, PinRoom: 1},
	}}
	r := Solve(in)
	assertNoDoubleBooking(t, in, r)
	if len(r.Unassigned) != 1 {
		t.Fatalf("beklenen tek atanamayan: %+v", r)
	}
}

func BenchmarkSolve120Rooms400Stays(b *testing.B) {
	rooms, adjacent := hotel()
	in := Input{Rooms: rooms, Adjacent: adjacent, Stays: workload(rooms, 400, 20, 13), Seed: 3}
	for i := 0; i < b.N; i++ {
		Solve(in)
	}
}

func TestPinnedInHouseGuestsStillMinimiseMoves(t *testing.T) {
	rooms, adjacent := hotel()
	var totalSolver, totalBase, worstMillis int
	for seed := int64(1); seed <= 20; seed++ {
		stays := workload(rooms, 400, 14, seed)
		rng := rand.New(rand.NewSource(seed * 31))
		byType := map[int64][]Room{}
		for _, r := range rooms {
			byType[r.TypeID] = append(byType[r.TypeID], r)
		}
		for i := range stays {
			if stays[i].From <= 2 && rng.Intn(2) == 0 {
				candidates := byType[stays[i].TypeID]
				stays[i].PinRoom = candidates[rng.Intn(len(candidates))].ID
			}
		}
		in := Input{Rooms: rooms, Adjacent: adjacent, Stays: stays, Seed: seed}
		r := Solve(in)
		base := FirstFit(in)
		assertNoDoubleBooking(t, in, r)
		totalSolver += r.Moves
		totalBase += base.Moves
		if int(r.ElapsedMS) > worstMillis {
			worstMillis = int(r.ElapsedMS)
		}
		if r.Moves > base.Moves {
			t.Fatalf("tohum %d: çözücü %d taşıma, taban çizgisi %d", seed, r.Moves, base.Moves)
		}
	}
	t.Logf("20 senaryoda toplam taşıma: çözücü=%d ilk-uygun=%d en kötü süre=%dms", totalSolver, totalBase, worstMillis)
}
