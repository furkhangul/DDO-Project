# Test Rehberi

## Hızlı doğrulama

```bash
cp .env.example .env
make verify
```

Komut sistemi ayağa kaldırır, demo verisini sıfırlar ve aşağıdaki kimlikleri API üzerinden uçtan uca dener. Sonunda PASS/FAIL tablosu basar, hata varsa sıfırdan farklı kodla çıkar. Yaklaşık 45 saniye sürer. Docker imajları yoksa ilk çalıştırmaya yaklaşık bir dakika eklenir.

Alternatif: `docker compose --profile verify run --rm verify` (sıfırlamadan çalışır, tekrar edilebilir).

Sonrasında arayüzü incelemek için `make reset-demo` çalıştırın, çünkü doğrulama gece denetimi de çalıştırır.

## Demo hesapları

Parola hepsi için `Konak!2026`.

| Rol | E-posta | Açılış ekranı |
|---|---|---|
| Resepsiyon | resepsiyon@konakpro.test | Panel |
| Rezervasyon | rezervasyon@konakpro.test | Panel |
| Kat Hizmetleri | kathizmeti@konakpro.test | Kat Hizmetleri |
| Gelir Yönetimi | gelir@konakpro.test | Panel |
| Genel Müdür | mudur@konakpro.test | Panel |
| Misafir | misafir@konakpro.test | Konaklamalarım |

## Kimlik tablosu

| Kimlik | Nasıl denenir | Beklenen sonuç |
|---|---|---|
| `HEALTH` | `curl localhost:8080/health` | `status: ok`, `database: ok`, otel günü |
| `AUTH-1` | Yanlış parolayla giriş | 401, hesabın varlığı sızmaz |
| `AUTH-2` | Oturumsuz `GET /api/v1/reservations` | 401 |
| `AUTH-3` | Refresh token'ı iki kez kullan | İkincisi 401, oturum ailesi kapanır |
| `RBAC-1` | Kat hizmetleri ile rapor, resepsiyon ile gece denetimi | 403 |
| `RBAC-2` | Misafir ile başkasının rezervasyonu | 403 |
| `F-1` | Oda tipi, oda, bitişiklik oluştur, `GET /rooms` | Kayıtlar kat, manzara ve bitişiklikle döner |
| `F-2` | Rezervasyon oluştur, müsaitlik ve takvime bak | `sold` artar, takvimde görünür |
| `F-3` | `GET /pricing`, kampanya oluştur, durdur | Sezon, tarife, kampanya döner |
| `F-4` | Bugüne rezervasyon yap, giriş ve çıkış dene | Erken giriş 409, giriş sonrası oda atanır, aynı gün çıkış 409 |
| `F-5` | Harcama, kısmi ödeme, aşırı iade, iade, %50 bölme | Aşırı iade 409, `balanced: true` |
| `F-6` | Oda kirli yap, görev aç, tamamla | Oda `clean` olur |
| `F-7` | Gece denetimi çalıştır | Otel günü bir ilerler, geçmişte `completed` görünür |
| `F-8` | `GET /reports/kpi?bucket=month` | Doluluk, ADR, RevPAR döner, RevPAR < ADR |
| `T-1` | 300 eşzamanlı rezervasyon, 10 oda, %10 overbooking | Tam 11 kabul, 289 `SOLD_OUT`, walk sonrası stok 10 |
| `T-2` | Min. konaklama, üç kez teklif, uyumsuz kod, kural değişikliği | `MIN_STAY`, aynı hash, `CAMPAIGN_INCOMPATIBLE`, garanti fiyat korunur |
| `T-3` | `POST /assignments/solve` 90 gün | 120+ oda, 400+ konaklama, 10 sn altı, çift atama yok |
| `T-4` | `crash_after: 3` ile denetim, sonra yeniden çalıştır | İlki 500, tarih ilerlemez, ikincisi `resumed: true`, çift ücret 0 |
| `T-5` | Karışık sıralı ve tekrarlı olaylar, kanal kapalıyken stok değişimi | En güncel durum uygulanır, mesaj ölü kuyruğa düşer, yeniden deneme başarılı |
| `IDEMP` | Aynı anahtarla iki ödeme, farklı gövdeyle üçüncü | Aynı yanıt, tek kayıt, üçüncüsü 422 |
| `API` | 404 hata biçimi, keyset sayfalama, metrik, OpenAPI | Tutarlı `error` nesnesi, sıralı sayfalar |

## Görev kanıt komutları

| Görev | Komut | Ne gösterir |
|---|---|---|
| `T-1` | `make verify` (T-1 satırı) | 300 eşzamanlı istekte stok aşılmaz |
| `T-1` | `go test -race ./internal/booking -run 'TestConcurrent\|TestDatabaseRejects\|TestOverlapping\|TestWalk'` | Yarış durumu ve veritabanı kısıtları |
| `T-2` | `go test ./internal/pricing -v` | Kurallar, determinizm, kuruş kaybı olmaması |
| `T-2` | `go test ./internal/booking -run 'TestStoredPrice\|TestStaleQuote'` | Rezervasyon anı fiyat garantisi |
| `T-3` | `go test ./internal/allocation -v` | 120 oda / 400 rezervasyon süresi, taşıma karşılaştırması |
| `T-4` | `go test ./internal/nightaudit ./internal/folio` | Çökme sonrası devam, folyo bütünlüğü |
| `T-4` | `make chaos` | Süreç öldürülünce kaldığı yerden devam |
| `T-5` | `go test ./internal/channel -v` | Tekrar, sıra, geri çekilme, ölü kuyruk |
| Yük | `make load` | p50, p95, p99 ve hata oranı |
| Kapsama | `make test` | Alan paketleri %70 eşiği |

Go testleri `backend/` klasöründen ve `TEST_DATABASE_URL` tanımlıyken çalışır. Go kurulu olmasa bile `make test` her şeyi Docker içinde çalıştırır.

## Arayüzde elle deneme

Başlamadan önce `make reset-demo`.

1. **Rezervasyon:** Resepsiyon ile giriş yapın. Rezervasyonlar → Yeni rezervasyon. Kampanya kodlarına `HOSGELDIN10, AILE5` yazın, Fiyatı hesapla ile gün gün dökümü görün, onaylayın.
2. **Uyumsuz kampanya:** Aynı formda `HOSGELDIN10, FLASH25` deneyin, reddedilir.
3. **Sürükle-bırak:** Oda Çizelgesi'nde bir mavi rezervasyonu aynı tipte başka bir odanın satırına sürükleyin. Dolu odaya bırakınca hata alınır.
4. **Check-in:** Panel → Bugün gelecekler → bir misafir → Giriş yap.
5. **Folyo:** Aynı ekranda Harcama ekle, Ödeme al, Böl (şirket adı ve %50), ödeme satırından İade.
6. **Check-out:** Bakiye sıfırlanmadan Çıkış yap reddedilir.
7. **Kat hizmetleri:** Kat Hizmetleri hesabıyla görevi Başla, sonra Bitir yapın. Telefon genişliğinde de kullanılabilir.
8. **Oda tahsisi:** Oda Tahsisi → Önizle → Planı uygula.
9. **Gece denetimi:** Gelir Yönetimi hesabıyla Gece Denetimi → Denetimi başlat. Bütünlük ihlali ve çift ücret 0 kalmalı.
10. **Raporlar:** Son 3 yıl seçilip doluluk, ADR ve RevPAR incelenir. Tablo bağlantısı aynı veriyi tablo olarak gösterir.
11. **Canlılık:** Panel'de Canlı akış ve Kanallar ekranı simülatör olaylarıyla kendiliğinden güncellenir.
12. **Misafir:** Misafir hesabıyla Konaklamalarım → bugünkü rezervasyon için Giriş yap (self check-in).
13. **Yetki:** Kat Hizmetleri hesabıyla adres çubuğuna `/raporlar` yazın, ana ekrana yönlendirilirsiniz. API aynı isteği 403 ile reddeder.

## Elle API denemesi

`requests.http` dosyası tüm uç noktaları içerir ve VS Code REST Client ile çalışır. Dosyanın başındaki giriş isteklerini çalıştırmak token'ları otomatik doldurur.
