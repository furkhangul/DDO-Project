# KonakPro — Otel Yönetim Sistemi


**Hazırlayan:** Furkan Gül

120 odalı bir otel için yönetim sistemi. Rezervasyon, oda tahsisi, giriş-çıkış, folyo, fiyatlandırma ve gece denetimi işlerini yapar.

## Kurulum

Bilgisayarda Docker kurulu olmalı.

```bash
git clone <repo-adresi> && cd <repo>
cp .env.example .env
docker compose up --build
```

- Arayüz: http://localhost:3000
- API: http://localhost:8080
- API belgesi: http://localhost:8080/openapi.yaml

Veritabanı ve örnek veriler otomatik yüklenir. Boş Docker önbelleğiyle sistem 55 saniyede açıldı.

## Demo hesapları

Tüm hesapların parolası: `Konak!2026`

| Rol | E-posta |
|---|---|
| Resepsiyon | resepsiyon@konakpro.test |
| Rezervasyon | rezervasyon@konakpro.test |
| Kat Hizmetleri | kathizmeti@konakpro.test |
| Gelir Yönetimi | gelir@konakpro.test |
| Genel Müdür | mudur@konakpro.test |
| Misafir | misafir@konakpro.test |

## Komutlar

| Komut | Ne yapar |
|---|---|
| `make verify` | Tüm özellikleri dener, PASS/FAIL tablosu yazar |
| `make reset-demo` | Verileri başlangıç haline getirir |
| `make test` | Testleri çalıştırır |
| `make load` | Yük testi yapar |
| `make chaos` | Servisleri durdurup sistemin toparlandığını gösterir |

`make verify` gece denetimi de çalıştırır. Sonrasında arayüzü incelemek için `make reset-demo` çalıştırın.

Test talimatları [TESTING.md](TESTING.md), mimari kararlar [DECISIONS.md](DECISIONS.md) dosyasındadır.

## Mimari

Sistem dört servisten oluşuyor: React arayüzü, Go API, PostgreSQL 16 ve kanal simülatörü. Simülatör, Booking gibi sitelerden gelen rezervasyonları taklit ediyor. Hepsi Docker Compose ile tek komutla açılıyor.

Backend'de chi, pgx, sqlc ve goose kullandım. Kilit ve kısıt içeren sorgular önemli olduğu için ORM yerine SQL'i açıkça yazmayı tercih ettim. Frontend'de React seçtim, çünkü oda çizelgesi ve canlı akış sürekli güncellenen ekranlar.

## Zorlu görevler

Aynı odanın iki kez satılmaması için her oda tipinin her gecesi için satış sayısı tutuluyor ve veritabanı kısıtı sınırın aşılmasını engelliyor. Otel fazla satılırsa misafir anlaşmalı başka bir otele yönlendirilebiliyor.

Fiyat hesabı sezon, hafta sonu, minimum konaklama, erken rezervasyon, çocuk fiyatı ve kampanyaları hesaba katıyor. Rezervasyon anındaki fiyat kaydedildiği için kurallar sonradan değişse de misafirin fiyatı değişmiyor.

Oda tahsisinde kat, manzara ve bitişik oda istekleri dikkate alınıyor, konaklama ortasında oda değişikliği en aza indiriliyor.

Gece denetimi konaklayan herkese gecelik ücret ve vergileri yazıyor. İşlem yarıda kesilirse kaldığı yerden sürüyor ve aynı gece iki kez ücret yazılamıyor.

Dış sitelerden gelen tekrarlı veya sırası karışık bildirimler aynı sonucu veriyor. Müsaitlik değişince siteler kuyruk üzerinden bilgilendiriliyor, gönderim başarısız olursa artan aralıklarla yeniden deneniyor.

## Ölçümler

### Yük testi

30 kullanıcı veri okurken saniyede 40 rezervasyon denemesi yapıldı (45 saniye, 8 çekirdek).

| İşlem | p50 | p95 | p99 | Hata oranı |
|---|---|---|---|---|
| Müsaitlik | 13,61 ms | 24,28 ms | 30,98 ms | %0 |
| Rezervasyon | 30,36 ms | 54,51 ms | 76,08 ms | %0 |
| Tümü (135.546 istek) | 9,15 ms | 21,46 ms | 31,31 ms | %0 |

### Test kapsamı

120 test fonksiyonu var. Çekirdek paketlerin toplam kapsamı %83,5, en düşük paket %74,4.

### EXPLAIN (ANALYZE)

Veri: 27.018 rezervasyon, 87.688 gece. Tüm çıktılar [docs/explain-analyze.txt](docs/explain-analyze.txt) dosyasındadır.

```text
Index Only Scan using reservation_nights_date_idx on reservation_nights n (actual rows=1429 loops=1)
  Index Cond: ((stay_date >= '2026-11-01'::date) AND (stay_date < '2026-12-01'::date))
  Heap Fetches: 0
Execution Time: 1.060 ms
```

## Doğrulama Soruları

### 1. En zorlandığın görev hangisiydi?

Oda tahsisi. Bazı odalar sabitlenince problem çok zorlaşıyor. Tam sayılı programlama 10 saniye sınırını garanti etmiyordu, tavlama benzetimi de bölünmüş konaklamaları birleştirmekte zayıf kaldı. Tek adımlı taşıma dolu otelde oda değişikliğini 34'ten ancak 26'ya indirdi. Odaları zincirleme kaydıran yöntem aynı senaryoda 12 değişiklik ve en fazla 1,57 saniye sonuç verdi.

### 2. Çözümün nerede yetersiz kalıyor?

- Sunucu, işlem bittikten sonra ama sonucu kaydetmeden kapanırsa aynı istek 60 saniye sonra tekrar çalışabilir.
- Oda tahsisi en iyi sonucu garanti etmez. Tam dolu senaryoda 0 mümkünken 12 oda değişikliği kaldı.
- Arızalı oda, satılabilir oda sayısını otomatik düşürmez.
- Dolu tarihe gelen dış site rezervasyonu elle çözülür.
- Gelmeyen misafirden ücret alınmaz.
- Giriş denemelerinde hız sınırı yoktur.

### 3. İki dizin ve ölçüm farkı

| Sorgu | Dizin | Dizinli | Dizinsiz |
|---|---|---|---|
| Gelecek 30 gecenin doluluğu | `reservation_nights_date_idx` | 1,06 ms | 5,58 ms |
| Geçmiş tarihli giriş araması | `reservations_arrivals_idx` | 0,05 ms | 2,51 ms |

`reservations_arrivals_idx` bugünkü gelenler sorgusunu hızlandırmadı, çünkü başka bir dizin bu sorguyu zaten kapsıyor. Faydası geçmiş tarih aramasında görülüyor.

### 4. Yapay zekâ kullanımı

Geliştirirken yapay zekâ aracı kullandım. Reddedilen çıktıya örnek olarak oda tahsisi testini verebilirim. İlk test geçiyordu ama sonuç `çözücü=0 ilk-uygun=0` idi, senaryo yeterince zor olmadığı için hiçbir şey kanıtlamıyordu.

Test tam dolu otel senaryosuyla yeniden yazıldı. Bu kez `çözücü=26 ilk-uygun=34` çıktı ve algoritmanın yetersiz olduğu görüldü. Algoritma düzeltilince sonuç 12'ye indi.

### 5. Overbooking çözümünün doğruluğunu nasıl kanıtladın?

10 oda ve %10 overbooking tanımlı bir oda tipine aynı anda 300 rezervasyon isteği gönderildi. Tam 11 rezervasyon kabul edildi, 289 istek reddedildi.

```bash
make verify
```

```text
T-1        PASS      0.26s  300 eşzamanlı rezervasyon, overbooking ve walk
```
