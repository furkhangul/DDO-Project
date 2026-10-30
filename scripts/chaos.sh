#!/bin/sh
set -u

API=${API_URL:-http://localhost:8080}
PASS=0
FAIL=0

step() { printf '\n== %s\n' "$1"; }
ok() { PASS=$((PASS + 1)); printf '   PASS %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '   FAIL %s\n' "$1"; }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 4 "$API/health"; }

wait_for() {
  want=$1
  limit=$2
  i=0
  while [ "$i" -lt "$limit" ]; do
    [ "$(code)" = "$want" ] && return 0
    sleep 1
    i=$((i + 1))
  done
  return 1
}

json() { sed -E "s/.*\"$1\":\"?([^\",}]*).*/\1/"; }

login() {
  curl -s "$API/api/v1/auth/login" -H 'content-type: application/json' \
    -d '{"email":"gelir@konakpro.test","password":"Konak!2026"}' | json access_token
}

step "1. Veritabanı durdurulur, API çökmeden 503 döner ve geri gelir"
wait_for 200 30 && ok "başlangıçta sistem sağlıklı" || bad "başlangıç"
docker compose stop db >/dev/null 2>&1
wait_for 503 20 && ok "veritabanı yokken /health 503" || bad "503 alınamadı"
docker compose ps api --format '{{.Status}}' | grep -q Up && ok "API süreci ayakta" || bad "API düştü"
docker compose start db >/dev/null 2>&1
wait_for 200 40 && ok "veritabanı dönünce /health 200" || bad "toparlanma olmadı"

step "2. Simülatör durdurulur, mesajlar yeniden denenir ve kuyruk boşalır"
TOKEN=$(login)
docker compose stop simulator >/dev/null 2>&1
DAY=$(curl -s "$API/health" | json business_date)
FROM=$(date -u -j -v+200d -f %Y-%m-%d "$DAY" +%Y-%m-%d 2>/dev/null || date -u -d "$DAY +200 days" +%Y-%m-%d)
TO=$(date -u -j -v+201d -f %Y-%m-%d "$DAY" +%Y-%m-%d 2>/dev/null || date -u -d "$DAY +201 days" +%Y-%m-%d)
MGR=$(curl -s "$API/api/v1/auth/login" -H 'content-type: application/json' -d '{"email":"mudur@konakpro.test","password":"Konak!2026"}' | json access_token)
curl -s -o /dev/null "$API/api/v1/reservations" -H "authorization: Bearer $MGR" -H 'content-type: application/json' \
  -d "{\"room_type_id\":1,\"rate_plan_id\":1,\"check_in\":\"$FROM\",\"check_out\":\"$TO\",\"adults\":2,\"guest\":{\"full_name\":\"Kaos\"}}"
sleep 3
PENDING=$(curl -s "$API/api/v1/channels/outbox?status=pending&limit=5" -H "authorization: Bearer $TOKEN" | grep -c last_error)
[ "$PENDING" -ge 1 ] && ok "kanal kapalıyken mesaj yeniden deneniyor" || bad "yeniden deneme görülmedi"
docker compose start simulator >/dev/null 2>&1
i=0
LEFT=1
while [ "$i" -lt 40 ]; do
  LEFT=$(curl -s "$API/api/v1/channels/outbox?status=pending&limit=200" -H "authorization: Bearer $TOKEN" | grep -o '"id":' | wc -l | tr -d ' ')
  [ "$LEFT" = "0" ] && break
  sleep 1
  i=$((i + 1))
done
[ "$LEFT" = "0" ] && ok "kanal dönünce kuyruk boşaldı" || bad "kuyrukta $LEFT mesaj kaldı"

step "3. Gece denetimi sırasında API süreci öldürülür, yeniden başlayınca kaldığı yerden devam eder"
BEFORE=$(curl -s "$API/health" | json business_date)
curl -s -o /dev/null --max-time 10 "$API/api/v1/night-audit/run" -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{"crash_after":5,"hard_exit":true}'
sleep 2
wait_for 200 40 && ok "API otomatik yeniden başladı" || bad "API dönmedi"
TOKEN=$(login)
MID=$(curl -s "$API/health" | json business_date)
[ "$MID" = "$BEFORE" ] && ok "yarıda kalan denetim otel gününü ilerletmedi" || bad "otel günü ilerledi"
RESULT=$(curl -s --max-time 30 "$API/api/v1/night-audit/run" -X POST -H "authorization: Bearer $TOKEN")
echo "$RESULT" | grep -q '"resumed":true' && ok "denetim kaldığı yerden sürdü: $RESULT" || bad "devam edilmedi: $RESULT"
STATE=$(curl -s "$API/api/v1/night-audit" -H "authorization: Bearer $TOKEN")
echo "$STATE" | grep -q '"duplicate_charges":0' && ok "çift ücret yok" || bad "çift ücret var"
echo "$STATE" | grep -q '"integrity_violations":0' && ok "folyo bütünlüğü korundu" || bad "bütünlük bozuldu"

printf '\nSonuç: %d başarılı, %d başarısız\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
