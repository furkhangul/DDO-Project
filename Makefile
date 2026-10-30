.PHONY: up down verify load reset-demo chaos test test-race coverage logs generate

GO_IMAGE := golang:1.26-alpine

up:
	@test -f .env || cp .env.example .env
	docker compose up --build -d --wait

down:
	docker compose down

reset-demo: up
	docker compose stop simulator
	docker compose run --rm seed /app/simulator reset
	docker compose start simulator

verify: reset-demo
	docker compose --profile verify run --rm --build verify

load: up
	docker compose run --rm --no-deps -e K6_API=http://api:8080 -v "$(PWD)/load:/load" --entrypoint k6 k6 run /load/booking.js

chaos: up
	sh scripts/chaos.sh

test:
	sh scripts/test.sh

coverage:
	sh scripts/test.sh coverage

logs:
	docker compose logs -f api simulator

generate:
	docker run --rm -v "$(PWD)/backend:/src" -w /src sqlc/sqlc:1.30.0 generate
