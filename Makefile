.PHONY: run demo build ui ui-dev test fmt db-up db-down db-psql db-test

DB_URL ?= postgres://assistant:assistant@localhost:5432/assistant?sslmode=disable

run:
	go run ./cmd/server

demo:
	DEMO_MODE=true go run ./cmd/server

ui:
	npm --prefix ui ci --no-audit --no-fund
	npm --prefix ui run build

# Live-reloading UI on :5173 (proxies /api to the Go server on :8080).
ui-dev:
	npm --prefix ui run dev

build: ui
	go build -o bin/server ./cmd/server

test:
	go test -race ./...

fmt:
	gofmt -w . && go vet ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

db-psql:
	docker compose exec postgres psql -U assistant -d assistant

db-test:
	TEST_DATABASE_URL='$(DB_URL)' go test ./internal/store -run Postgres -v -count=1
