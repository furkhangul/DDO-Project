# Mimari Karar Kayıtları

## 1. Stok sayacı ve EXCLUDE birlikte kullanılır

**Bağlam:** Müşteri oda tipi satın alır, oda numarası sonradan verilir. Yalnızca `EXCLUDE` kullanılırsa satış anında oda seçmek gerekir, bu da kontrollü overbooking'i imkânsız kılar.

**Karar:** Satış `inventory.sold` sayacı ve `CHECK` kısıtıyla, fiziksel çakışma `room_assignments` üzerindeki `EXCLUDE` kısıtıyla korunur.

**Sonuç:** Fazla satış mümkün ama sınırlıdır. Bedeli, sayacın gerçek rezervasyonlarla senkron tutulmasıdır. Tüm değişimler tek noktadan (`ReserveNights`, `ReleaseNights`) geçer.

## 2. SERIALIZABLE yerine sıralı satır kilidi

**Bağlam:** `SERIALIZABLE` + yeniden deneme yoğun satışta çok sayıda iptal üretir. 300 eşzamanlı istek aynı satırlara yöneldiğinde yeniden deneme fırtınası oluşur.

**Karar:** `READ COMMITTED` ve tarih sırasıyla `SELECT ... FOR UPDATE`. Sıra sabit olduğu için kilitlenme oluşmaz. `InTx` yine de `40P01` ve `40001` için yeniden dener.

**Sonuç:** Aynı oda tipi ve gecede satışlar sırayla ilerler. Ölçülen rezervasyon p95 değeri 54,51 ms'dir. Darboğaz aynı gecenin satırıdır, otelin tamamı değil.

## 3. Rezervasyon anında kural anlık görüntüsü

**Bağlam:** "Aynı rezervasyon her hesaplamada aynı sonucu vermeli". Kural tablolarını sürümlemek karmaşıktır, tüm sorgular tarih filtresi gerektirir.

**Karar:** İlgili kurallar ve girdi JSON olarak rezervasyona yazılır, gecelik tutarlar `reservation_nights` tablosunda saklanır. Gece denetimi yeniden hesap yapmaz, saklı tutarı kullanır.

**Sonuç:** Doğrulama basit, geçmiş her zaman yeniden üretilebilir. Bedeli, satır başına birkaç KB ek depolamadır.

## 4. Durum taşıyan kanal olayları ve sıra numarası

**Bağlam:** Olaylar tekrarlı ve sırasız gelir. Fark tabanlı olaylarda `modify` önce gelirse uygulanacak bir kayıt yoktur, bekletmek gerekir.

**Karar:** Her olay rezervasyonun tam durumunu taşır, `last_seq` üzerinden en yüksek sıra kazanır. İptal terminaldir.

**Sonuç:** Bekleme kuyruğu gerekmez ve olaylar hangi sırayla gelirse gelsin aynı sonuç oluşur. Bunu 6 farklı karıştırmayla çalışan `TestAnyArrivalOrderConvergesToSameState` kanıtlar.

## 5. Tetikleyici ile transactional outbox

**Bağlam:** Uygulama stok değişiminden sonra kanala yazarsa araya giren çökme mesajı kaybettirir. Her çağrı noktasına kod eklemek de unutulmaya açıktır.

**Karar:** `inventory` üzerindeki tetikleyici aynı transaction içinde `channel_outbox`'a yazar. Bekleyen satırlar kısmi benzersiz dizinle birleştirilir. Mesaj içeriği gönderim anında güncel stoktan okunur.

**Sonuç:** Mesaj kaybı olmaz, bir gecede 50 değişiklik tek mesaja düşer ve mesaj sırası önemsizleşir. Bedeli, iş kuralının bir kısmının SQL'de yaşamasıdır.

## 6. Gece denetiminde kalem bazlı transaction

**Bağlam:** Tek büyük transaction çökerse tüm iş kaybolur, uzun süre satır kilidi tutar ve resepsiyonu bloklar.

**Karar:** Her konaklama ayrı transaction, ilerleme `night_audit_items` tablosunda. Çift çalışma session advisory lock ile engellenir. Süreç ölünce kilit kendiliğinden bırakılır.

**Sonuç:** Devam etme doğal hale gelir. Benzersiz dizin ikinci güvenlik katmanıdır. Otel günü yalnızca son transaction'da ilerler.

## 7. Bakiye tetikleyiciyle tutulur, kalemler değiştirilemez

**Bağlam:** "Folyo toplamı kalemlerin toplamına her zaman eşit olmalı." Her sorguda toplamak yavaştır, uygulamada elle güncellemek yarış durumuna açıktır.

**Karar:** `folio_lines` eklemeli-yalnızdır, `balance_kurus` tetikleyiciyle güncellenir ve doğrudan `UPDATE` reddedilir. Bölme işlemi kalem değiştirmek yerine eşleşen iki aktarım satırı yazar.

**Sonuç:** Muhasebe izi korunur, hatalar ters kayıtla düzeltilir. `folio_integrity` görünümü bu eşitliği sürekli denetler.

## 8. Frontend için React, UI kütüphanesi yok

**Bağlam:** Şartname serbest bırakıyor. Sürükle-bırak çizelge ve canlı akış gibi durumlu ekranlar var.

**Karar:** React + TypeScript + TanStack Query. Bileşen kütüphanesi kullanılmadı, çünkü oda çizelgesi yoğun ve özel bir ekran olduğu için hazır bileşenlerden çok az faydalanılırdı.

**Sonuç:** Ana paket 95 kB (gzip). Sayfalar tembel yüklenir. Bedeli, form ve tablo stillerinin elle yazılmasıdır.

## 9. Otel günü sistem saatinden bağımsızdır

**Bağlam:** Gerçek PMS'lerde gün, gece denetimiyle kapanır. Gece 01.30'da yapılan giriş bir önceki otel gününe aittir.

**Karar:** `hotel_state.business_date` tek doğruluk kaynağıdır, tüm iş kuralları bunu kullanır. Seed sabit başlangıç gününe bağlıdır.

**Sonuç:** Aynı tohum aynı veriyi üretir ve testler saate bağlı kalmaz. Demoda takvim gerçek tarihle örtüşmeyebilir.
