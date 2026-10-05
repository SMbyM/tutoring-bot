TEST_DATABASE_URL ?= postgres://bot:bot@localhost:5433/bot_test?sslmode=disable

.PHONY: run-tg run-worker generate test test-db lint up down

run-tg:
	go run ./cmd/tg

run-worker:
	go run ./cmd/worker

# Код доступа к БД из store/queries/*.sql (нужен sqlc 1.30+)
generate:
	sqlc generate

# Юнит-тесты без базы
test:
	go test ./...

# Полный прогон с PostgreSQL (база очищается!)
test-db:
	docker run -d --rm --name bot-test-db -e POSTGRES_USER=bot -e POSTGRES_PASSWORD=bot -e POSTGRES_DB=bot_test -p 5433:5432 postgres:16-alpine
	sleep 3
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -p 1 -race -cover ./... ; status=$$?; docker stop bot-test-db; exit $$status

lint:
	sqlc diff && gofmt -l . && go vet ./...

up:
	docker compose up -d --build

down:
	docker compose down
