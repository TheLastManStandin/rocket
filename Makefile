.PHONY: db dev build test tidy front telegram

db:
	docker compose up -d

test:
	go test ./...

build: front
	go build -o bin/racketka ./cmd/server

front:
	cd web && npm install && npm run build

dev:
	go run ./cmd/server

telegram:
	./scripts/telegram.sh

tidy:
	go mod tidy && gofmt -w .
