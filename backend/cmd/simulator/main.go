package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"konakpro/internal/channel"
	"konakpro/internal/db/migrations"
	"konakpro/internal/platform/store"
	"konakpro/internal/seed"
)

type settings struct {
	databaseURL string
	apiURL      string
	secret      string
	addr        string
	seed        int64
	anchor      time.Time
	interval    time.Duration
}

func load() (settings, error) {
	s := settings{
		databaseURL: os.Getenv("DATABASE_URL"),
		apiURL:      fallback(os.Getenv("API_URL"), "http://api:8080"),
		secret:      os.Getenv("CHANNEL_SECRET"),
		addr:        fallback(os.Getenv("SIMULATOR_ADDR"), ":9090"),
		seed:        42,
		interval:    4 * time.Second,
	}
	if value, err := strconv.ParseInt(os.Getenv("SEED"), 10, 64); err == nil {
		s.seed = value
	}
	if value, err := time.ParseDuration(os.Getenv("SIM_EVENT_INTERVAL")); err == nil {
		s.interval = value
	}
	anchor, err := time.Parse(time.DateOnly, fallback(os.Getenv("SEED_ANCHOR_DATE"), "2026-11-01"))
	if err != nil {
		return s, fmt.Errorf("SEED_ANCHOR_DATE: %w", err)
	}
	s.anchor = anchor
	return s, nil
}

func fallback(value, other string) string {
	if value != "" {
		return value
	}
	return other
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mode := "run"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	cfg, err := load()
	if err != nil {
		log.Error("yapılandırma hatası", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch mode {
	case "seed":
		err = seedDatabase(ctx, log, cfg, false)
	case "reset":
		err = seedDatabase(ctx, log, cfg, true)
	case "run":
		err = run(ctx, log, cfg)
	case "healthcheck":
		err = healthcheck(cfg.addr)
	default:
		err = fmt.Errorf("bilinmeyen komut: %s", mode)
	}
	if err != nil {
		log.Error("simülatör durdu", "error", err)
		os.Exit(1)
	}
}

func healthcheck(addr string) error {
	response, err := http.Get("http://127.0.0.1" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("durum %d", response.StatusCode)
	}
	return nil
}

func seedDatabase(ctx context.Context, log *slog.Logger, cfg settings, reset bool) error {
	if err := migrations.Up(cfg.databaseURL); err != nil {
		return err
	}
	pool, err := store.Connect(ctx, cfg.databaseURL, 4)
	if err != nil {
		return err
	}
	defer pool.Close()
	if reset {
		if err := seed.Reset(ctx, pool); err != nil {
			return err
		}
	}
	done, digest, err := seed.Seeded(ctx, pool)
	if err != nil {
		return err
	}
	if done {
		log.Info("veri zaten yüklü", "digest", digest)
		return nil
	}
	started := time.Now()
	data, err := seed.Build(cfg.seed, cfg.anchor)
	if err != nil {
		return err
	}
	if err := seed.Load(ctx, pool, data, cfg.secret); err != nil {
		return err
	}
	log.Info("veri yüklendi", "seed", cfg.seed, "digest", data.Digest, "reservations", len(data.Reservations), "nights", len(data.Nights), "ms", time.Since(started).Milliseconds())
	return nil
}

type control struct {
	failRate  atomic.Int64
	delivered atomic.Int64
	rejected  atomic.Int64
	mu        sync.Mutex
	latest    map[string]channel.ARI
}

func (c *control) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /ari", func(w http.ResponseWriter, r *http.Request) {
		if rand.Int63n(100) < c.failRate.Load() {
			c.rejected.Add(1)
			http.Error(w, "kanal geçici olarak yanıt vermiyor", http.StatusServiceUnavailable)
			return
		}
		var ari channel.ARI
		if err := json.NewDecoder(r.Body).Decode(&ari); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.delivered.Add(1)
		c.mu.Lock()
		c.latest[ari.Channel+"/"+ari.RoomType+"/"+ari.Date] = ari
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /control", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FailRate int64 `json:"fail_rate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.failRate.Store(body.FailRate)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{
			"delivered": c.delivered.Load(), "rejected": c.rejected.Load(), "fail_rate": c.failRate.Load(),
		})
	})
	return mux
}

type booking struct {
	event channel.Event
	code  string
}

func run(ctx context.Context, log *slog.Logger, cfg settings) error {
	ctl := &control{latest: map[string]channel.ARI{}}
	server := &http.Server{Addr: cfg.addr, Handler: ctl.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("simülatör sunucusu", "error", err)
		}
	}()

	rng := rand.New(rand.NewSource(cfg.seed + 1))
	client := &http.Client{Timeout: 5 * time.Second}
	channels := []string{"booking", "expedia", "tatilbudur"}
	types := []string{"STD", "STD", "STD", "DLX", "DLX", "FAM", "SUI"}
	run := time.Now().Unix()
	var open []booking
	var eventID, ref int64

	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		businessDate, err := fetchBusinessDate(ctx, client, cfg.apiURL)
		if err != nil {
			log.Warn("api henüz hazır değil", "error", err)
			continue
		}
		var event channel.Event
		var code string
		roll := rng.Intn(100)
		switch {
		case roll < 25 && len(open) > 0:
			index := rng.Intn(len(open))
			b := &open[index]
			b.event.Seq++
			b.event.Type = "modify"
			b.event.CheckOut = shift(b.event.CheckOut, 1)
			b.event.NetKurus += 180000
			event, code = b.event, b.code
		case roll < 40 && len(open) > 0:
			index := rng.Intn(len(open))
			b := open[index]
			b.event.Seq++
			b.event.Type = "cancel"
			event, code = b.event, b.code
			open = append(open[:index], open[index+1:]...)
		default:
			ref++
			lead := 2 + rng.Intn(60)
			nights := 1 + rng.Intn(5)
			from := businessDate.AddDate(0, 0, lead)
			event = channel.Event{
				ExternalRef: fmt.Sprintf("OTA-%d-%05d", run, ref), Seq: 1, Type: "create",
				RoomType: types[rng.Intn(len(types))], CheckIn: from.Format(time.DateOnly),
				CheckOut: from.AddDate(0, 0, nights).Format(time.DateOnly), Adults: 1 + rng.Intn(2),
				GuestName: "Kanal Misafiri " + strconv.FormatInt(ref, 10), NetKurus: int64(nights) * 360000,
			}
			code = channels[rng.Intn(len(channels))]
			open = append(open, booking{event: event, code: code})
			if len(open) > 40 {
				open = open[1:]
			}
		}
		eventID++
		event.EventID = fmt.Sprintf("%d-%d", run, eventID)
		delay := time.Duration(rng.Intn(3000)) * time.Millisecond
		duplicate := rng.Intn(100) < 20
		go deliver(ctx, log, client, cfg, code, event, delay)
		if duplicate {
			go deliver(ctx, log, client, cfg, code, event, delay+time.Duration(rng.Intn(2000))*time.Millisecond)
		}
	}
}

func shift(day string, days int) string {
	parsed, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return day
	}
	return parsed.AddDate(0, 0, days).Format(time.DateOnly)
}

func fetchBusinessDate(ctx context.Context, client *http.Client, base string) (time.Time, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return time.Time{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return time.Time{}, err
	}
	defer response.Body.Close()
	var body struct {
		BusinessDate string `json:"business_date"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.DateOnly, body.BusinessDate)
}

func deliver(ctx context.Context, log *slog.Logger, client *http.Client, cfg settings, code string, event channel.Event, delay time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.apiURL+"/api/v1/channels/"+code+"/events", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Channel-Signature", channel.Sign(cfg.secret, body))
	response, err := client.Do(request)
	if err != nil {
		log.Warn("olay gönderilemedi", "event", event.EventID, "error", err)
		return
	}
	defer response.Body.Close()
	log.Info("olay iletildi", "event", event.EventID, "type", event.Type, "seq", event.Seq, "status", response.StatusCode)
}
