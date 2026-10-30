#!/bin/sh
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
NETWORK=konakpro-test
DB=konakpro-test-db

cleanup() {
  docker rm -f "$DB" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker network create "$NETWORK" >/dev/null
docker run -d --name "$DB" --network "$NETWORK" -e POSTGRES_USER=konak -e POSTGRES_PASSWORD=konak -e POSTGRES_DB=konak_test \
  --tmpfs /var/lib/postgresql/data postgres:16-alpine -c max_connections=400 -c fsync=off >/dev/null
until docker exec "$DB" pg_isready -U konak -d konak_test >/dev/null 2>&1; do sleep 1; done

docker run --rm --network "$NETWORK" -v "$ROOT:/repo" -v konakpro-gomod:/go/pkg/mod -w /repo/backend \
  -e TEST_DATABASE_URL="postgres://konak:konak@$DB:5432/konak_test?sslmode=disable" -e CGO_ENABLED=1 \
  golang:1.26-alpine sh -c '
    set -e
    apk add -q gcc musl-dev
    go test -race -short -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./internal/... > test-output.txt 2>&1 || { cat test-output.txt; exit 1; }
    grep -E "^(ok|FAIL)" test-output.txt
    sh ../scripts/coverage-gate.sh coverage.out
    go tool cover -func=coverage.out | tail -1
    rm -f test-output.txt
  '
