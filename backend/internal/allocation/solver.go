package allocation

import (
	"math/rand"
	"slices"
	"sort"
	"time"
)

const (
	costMove     = 1000
	costFloor    = 30
	costView     = 40
	costAdjacent = 60
	costGap      = 1

	defaultIterations = 150000
	maxAugmentNodes   = 4000
	maxBlockers       = 3
	mergePasses       = 40
)

type Room struct {
	ID     int64  `json:"id"`
	TypeID int64  `json:"type_id"`
	Floor  int    `json:"floor"`
	View   string `json:"view"`
}

type Stay struct {
	ID        int64  `json:"id"`
	TypeID    int64  `json:"type_id"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	PrefFloor *int   `json:"pref_floor,omitempty"`
	PrefView  string `json:"pref_view,omitempty"`
	Group     string `json:"group,omitempty"`
	PinRoom   int64  `json:"pin_room,omitempty"`
}

type Input struct {
	Rooms      []Room
	Stays      []Stay
	Adjacent   [][2]int64
	Seed       int64
	Iterations int
	Budget     time.Duration
}

type Segment struct {
	StayID int64 `json:"stay_id"`
	RoomID int64 `json:"room_id"`
	From   int   `json:"from"`
	To     int   `json:"to"`
}

type Result struct {
	Segments    []Segment `json:"segments"`
	Unassigned  []int64   `json:"unassigned"`
	Moves       int       `json:"moves"`
	PrefsTotal  int       `json:"prefs_total"`
	PrefsMet    int       `json:"prefs_met"`
	Iterations  int       `json:"iterations"`
	ElapsedMS   int64     `json:"elapsed_ms"`
	InitialCost int       `json:"initial_cost"`
	FinalCost   int       `json:"final_cost"`
}

type part struct{ room, from, to int }

type state struct {
	in       Input
	roomIdx  map[int64]int
	byType   map[int64][]int
	adjacent map[[2]int]bool
	grid     [][]int32
	stays    []Stay
	parts    [][]part
	groups   map[string][]int
	horizon  int
	offset   int
	journal  []change
	budget   int
}

func Solve(in Input) Result {
	started := time.Now()
	s := newState(in)
	s.placePinned()
	s.placeGreedy()
	initial := s.totalCost()
	iterations := s.localSearch(started)

	result := s.result()
	result.Iterations = iterations
	result.ElapsedMS = time.Since(started).Milliseconds()
	result.InitialCost = initial
	result.FinalCost = s.totalCost()
	return result
}

func FirstFit(in Input) Result {
	s := newState(in)
	s.placePinned()
	for _, stay := range s.order() {
		if s.stays[stay].PinRoom != 0 {
			continue
		}
		if room, ok := s.firstFree(stay); ok {
			s.assign(stay, []part{{room, s.stays[stay].From, s.stays[stay].To}})
			continue
		}
		s.placeSplit(stay)
	}
	return s.result()
}

func newState(in Input) *state {
	s := &state{
		in:       in,
		roomIdx:  make(map[int64]int, len(in.Rooms)),
		byType:   make(map[int64][]int),
		adjacent: make(map[[2]int]bool, len(in.Adjacent)*2),
		groups:   make(map[string][]int),
		stays:    slices.Clone(in.Stays),
		parts:    make([][]part, len(in.Stays)),
	}
	sort.SliceStable(s.stays, func(i, j int) bool { return s.stays[i].ID < s.stays[j].ID })
	for i, room := range in.Rooms {
		s.roomIdx[room.ID] = i
		s.byType[room.TypeID] = append(s.byType[room.TypeID], i)
	}
	for _, pair := range in.Adjacent {
		a, okA := s.roomIdx[pair[0]]
		b, okB := s.roomIdx[pair[1]]
		if okA && okB {
			s.adjacent[[2]int{a, b}] = true
			s.adjacent[[2]int{b, a}] = true
		}
	}
	lo, hi := 0, 0
	for i, stay := range s.stays {
		if i == 0 || stay.From < lo {
			lo = stay.From
		}
		if i == 0 || stay.To > hi {
			hi = stay.To
		}
		if stay.Group != "" {
			s.groups[stay.Group] = append(s.groups[stay.Group], i)
		}
	}
	s.offset, s.horizon = lo, hi-lo
	s.grid = make([][]int32, len(in.Rooms))
	for i := range s.grid {
		s.grid[i] = make([]int32, s.horizon)
		for day := range s.grid[i] {
			s.grid[i][day] = -1
		}
	}
	return s
}

func (s *state) order() []int {
	order := make([]int, len(s.stays))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := s.stays[order[a]], s.stays[order[b]]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.To != y.To {
			return x.To > y.To
		}
		return x.ID < y.ID
	})
	return order
}

func (s *state) placePinned() {
	for i, stay := range s.stays {
		if stay.PinRoom == 0 {
			continue
		}
		if room, ok := s.roomIdx[stay.PinRoom]; ok && s.free(room, stay.From, stay.To, i) {
			s.assign(i, []part{{room, stay.From, stay.To}})
		}
	}
}

func (s *state) placeGreedy() {
	for _, stay := range s.order() {
		if s.stays[stay].PinRoom != 0 {
			continue
		}
		if room, ok := s.bestWhole(stay); ok {
			s.assign(stay, []part{{room, s.stays[stay].From, s.stays[stay].To}})
			continue
		}
		if s.augment(stay, 4, nil) {
			continue
		}
		s.rollback(0)
		s.placeSplit(stay)
	}
}

func (s *state) free(room, from, to, ignore int) bool {
	row := s.grid[room]
	for day := from - s.offset; day < to-s.offset; day++ {
		if row[day] != -1 && int(row[day]) != ignore {
			return false
		}
	}
	return true
}

func (s *state) firstFree(stay int) (int, bool) {
	st := s.stays[stay]
	for _, room := range s.byType[st.TypeID] {
		if s.free(room, st.From, st.To, -1) {
			return room, true
		}
	}
	return 0, false
}

func (s *state) bestWhole(stay int) (int, bool) {
	st := s.stays[stay]
	best, bestCost := -1, 0
	for _, room := range s.byType[st.TypeID] {
		if !s.free(room, st.From, st.To, -1) {
			continue
		}
		cost := s.placementCost(stay, room)
		if best == -1 || cost < bestCost {
			best, bestCost = room, cost
		}
	}
	return best, best != -1
}

func (s *state) placementCost(stay, room int) int {
	return s.prefCost(stay, room) + s.adjacencyCost(stay, room) + s.gapCost(room, s.stays[stay].From, s.stays[stay].To)
}

func (s *state) prefCost(stay, room int) int {
	st := s.stays[stay]
	info := s.in.Rooms[room]
	cost := 0
	if st.PrefFloor != nil && *st.PrefFloor != info.Floor {
		cost += costFloor
	}
	if st.PrefView != "" && st.PrefView != info.View {
		cost += costView
	}
	return cost
}

func (s *state) adjacencyCost(stay, room int) int {
	st := s.stays[stay]
	if st.Group == "" {
		return 0
	}
	placedMates, near := 0, 0
	for _, mate := range s.groups[st.Group] {
		if mate == stay || len(s.parts[mate]) == 0 {
			continue
		}
		placedMates++
		for _, p := range s.parts[mate] {
			if s.adjacent[[2]int{room, p.room}] {
				near++
				break
			}
		}
	}
	if placedMates > 0 && near == 0 {
		return costAdjacent
	}
	return 0
}

func (s *state) gapCost(room, from, to int) int {
	row := s.grid[room]
	gap := 0
	for day := from - s.offset - 1; day >= 0 && row[day] == -1; day-- {
		gap++
	}
	for day := to - s.offset; day < s.horizon && row[day] == -1; day++ {
		gap++
	}
	return gap * costGap
}

func (s *state) assign(stay int, parts []part) {
	s.parts[stay] = parts
	for _, p := range parts {
		for day := p.from - s.offset; day < p.to-s.offset; day++ {
			s.grid[p.room][day] = int32(stay)
		}
	}
}

func (s *state) release(stay int) []part {
	parts := s.parts[stay]
	for _, p := range parts {
		for day := p.from - s.offset; day < p.to-s.offset; day++ {
			s.grid[p.room][day] = -1
		}
	}
	s.parts[stay] = nil
	return parts
}

type change struct {
	stay  int
	parts []part
}

func (s *state) place(stay int, parts []part) {
	s.journal = append(s.journal, change{stay, s.parts[stay]})
	s.release(stay)
	if parts != nil {
		s.assign(stay, parts)
	}
}

func (s *state) rollback(mark int) {
	for i := len(s.journal) - 1; i >= mark; i-- {
		c := s.journal[i]
		s.release(c.stay)
		if c.parts != nil {
			s.assign(c.stay, c.parts)
		}
	}
	s.journal = s.journal[:mark]
}

func (s *state) augment(stay, depth int, rng *rand.Rand) bool {
	s.budget = maxAugmentNodes
	s.journal = s.journal[:0]
	visited := map[int]bool{stay: true}
	s.place(stay, nil)
	return s.search(stay, depth, visited, rng)
}

func (s *state) search(stay, depth int, visited map[int]bool, rng *rand.Rand) bool {
	if room, ok := s.bestWhole(stay); ok {
		s.place(stay, []part{{room, s.stays[stay].From, s.stays[stay].To}})
		return true
	}
	if depth == 0 || s.budget <= 0 {
		return false
	}
	st := s.stays[stay]
	rooms := slices.Clone(s.byType[st.TypeID])
	if rng != nil {
		rng.Shuffle(len(rooms), func(i, j int) { rooms[i], rooms[j] = rooms[j], rooms[i] })
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		return len(s.blockers(rooms[i], st.From, st.To)) < len(s.blockers(rooms[j], st.From, st.To))
	})
	for _, room := range rooms {
		blockers := s.blockers(room, st.From, st.To)
		if len(blockers) == 0 || len(blockers) > maxBlockers || anyVisited(blockers, visited) {
			continue
		}
		s.budget--
		mark := len(s.journal)
		for _, b := range blockers {
			visited[b] = true
			s.place(b, nil)
		}
		s.place(stay, []part{{room, st.From, st.To}})
		ok := true
		for _, b := range blockers {
			if !s.search(b, depth-1, visited, rng) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
		s.rollback(mark)
		for _, b := range blockers {
			delete(visited, b)
		}
	}
	return false
}

func anyVisited(stays []int, visited map[int]bool) bool {
	return slices.ContainsFunc(stays, func(stay int) bool { return visited[stay] })
}

func (s *state) blockers(room, from, to int) []int {
	var found []int
	row := s.grid[room]
	for day := from - s.offset; day < to-s.offset; day++ {
		if occupant := int(row[day]); occupant != -1 && !slices.Contains(found, occupant) {
			if s.stays[occupant].PinRoom != 0 {
				return nil
			}
			found = append(found, occupant)
		}
	}
	return found
}

func (s *state) tryRelocate(stay, room int, blockers []int) bool {
	saved := make([][]part, len(blockers))
	for i, b := range blockers {
		saved[i] = s.release(b)
	}
	st := s.stays[stay]
	s.assign(stay, []part{{room, st.From, st.To}})
	for i, b := range blockers {
		target, ok := s.bestWhole(b)
		if !ok {
			s.release(stay)
			for j := 0; j < i; j++ {
				s.release(blockers[j])
			}
			for j, parts := range saved {
				s.assign(blockers[j], parts)
			}
			return false
		}
		s.assign(b, []part{{target, s.stays[b].From, s.stays[b].To}})
	}
	return true
}

func (s *state) placeSplit(stay int) {
	st := s.stays[stay]
	var parts []part
	for day := st.From; day < st.To; {
		bestRoom, bestEnd := -1, day
		for _, room := range s.byType[st.TypeID] {
			end := day
			for end < st.To && s.grid[room][end-s.offset] == -1 {
				end++
			}
			if end > bestEnd {
				bestRoom, bestEnd = room, end
			}
		}
		if bestRoom == -1 {
			return
		}
		parts = append(parts, part{bestRoom, day, bestEnd})
		day = bestEnd
	}
	s.assign(stay, parts)
}

func (s *state) stayCost(stay int) int {
	parts := s.parts[stay]
	if len(parts) == 0 {
		return 0
	}
	cost := (len(parts) - 1) * costMove
	worst := 0
	for _, p := range parts {
		if c := s.prefCost(stay, p.room); c > worst {
			worst = c
		}
	}
	cost += worst
	return cost + s.adjacencyCost(stay, parts[0].room)
}

func (s *state) totalCost() int {
	total := 0
	for i := range s.stays {
		total += s.stayCost(i)
	}
	return total
}

func (s *state) neighbourhoodCost(a, b int) int {
	cost := s.stayCost(a)
	if b >= 0 {
		cost += s.stayCost(b)
	}
	seen := map[string]bool{}
	for _, stay := range []int{a, b} {
		if stay < 0 || s.stays[stay].Group == "" || seen[s.stays[stay].Group] {
			continue
		}
		seen[s.stays[stay].Group] = true
		for _, mate := range s.groups[s.stays[stay].Group] {
			if mate != a && mate != b {
				cost += s.stayCost(mate)
			}
		}
	}
	return cost
}

func (s *state) localSearch(started time.Time) int {
	limit := s.in.Iterations
	if limit == 0 {
		limit = defaultIterations
	}
	budget := s.in.Budget
	if budget == 0 {
		budget = 8 * time.Second
	}
	movable := make([]int, 0, len(s.stays))
	for i, stay := range s.stays {
		if stay.PinRoom == 0 && len(s.parts[i]) > 0 {
			movable = append(movable, i)
		}
	}
	if len(movable) == 0 {
		return 0
	}
	rng := rand.New(rand.NewSource(s.in.Seed))
	iterations := 0
	for pass := 0; pass < mergePasses && time.Since(started) < budget/2; pass++ {
		for _, stay := range movable {
			if len(s.parts[stay]) > 1 {
				s.tryMerge(stay, rng)
			}
		}
	}
	for ; iterations < limit; iterations++ {
		if iterations%1024 == 0 && time.Since(started) > budget {
			break
		}
		stay := movable[rng.Intn(len(movable))]
		if len(s.parts[stay]) != 1 {
			continue
		}
		rooms := s.byType[s.stays[stay].TypeID]
		s.tryMove(stay, rooms[rng.Intn(len(rooms))])
	}
	return iterations
}

func (s *state) tryMerge(stay int, rng *rand.Rand) {
	if !s.augment(stay, 8, rng) {
		s.rollback(0)
	}
}

func (s *state) tryMove(stay, target int) {
	st := s.stays[stay]
	current := s.parts[stay][0]
	if target == current.room {
		return
	}
	blockers := s.blockers(target, st.From, st.To)
	switch {
	case s.free(target, st.From, st.To, -1):
		before := s.neighbourhoodCost(stay, -1)
		s.release(stay)
		s.assign(stay, []part{{target, st.From, st.To}})
		if s.neighbourhoodCost(stay, -1) > before {
			s.release(stay)
			s.assign(stay, []part{current})
		}
	case len(blockers) == 1:
		other := blockers[0]
		o := s.stays[other]
		if len(s.parts[other]) != 1 {
			return
		}
		otherPart := s.parts[other][0]
		before := s.neighbourhoodCost(stay, other)
		s.release(stay)
		s.release(other)
		if !s.free(target, st.From, st.To, -1) || !s.free(current.room, o.From, o.To, -1) {
			s.assign(stay, []part{current})
			s.assign(other, []part{otherPart})
			return
		}
		s.assign(stay, []part{{target, st.From, st.To}})
		s.assign(other, []part{{current.room, o.From, o.To}})
		if s.neighbourhoodCost(stay, other) > before {
			s.release(stay)
			s.release(other)
			s.assign(stay, []part{current})
			s.assign(other, []part{otherPart})
		}
	}
}

func (s *state) result() Result {
	r := Result{Segments: []Segment{}, Unassigned: []int64{}}
	for i, stay := range s.stays {
		parts := s.parts[i]
		if len(parts) == 0 {
			r.Unassigned = append(r.Unassigned, stay.ID)
			continue
		}
		r.Moves += len(parts) - 1
		for _, p := range parts {
			r.Segments = append(r.Segments, Segment{stay.ID, s.in.Rooms[p.room].ID, p.from, p.to})
		}
		s.tallyPrefs(i, &r)
	}
	return r
}

func (s *state) tallyPrefs(stay int, r *Result) {
	st := s.stays[stay]
	if st.PrefFloor != nil {
		r.PrefsTotal++
		if s.allParts(stay, func(room Room) bool { return room.Floor == *st.PrefFloor }) {
			r.PrefsMet++
		}
	}
	if st.PrefView != "" {
		r.PrefsTotal++
		if s.allParts(stay, func(room Room) bool { return room.View == st.PrefView }) {
			r.PrefsMet++
		}
	}
	if st.Group != "" && len(s.groups[st.Group]) > 1 {
		r.PrefsTotal++
		if s.adjacencyCost(stay, s.parts[stay][0].room) == 0 {
			r.PrefsMet++
		}
	}
}

func (s *state) allParts(stay int, ok func(Room) bool) bool {
	for _, p := range s.parts[stay] {
		if !ok(s.in.Rooms[p.room]) {
			return false
		}
	}
	return true
}
