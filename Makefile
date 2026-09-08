.PHONY: db dev build test tidy front telegram

# --wait holds until the healthcheck passes rather than until the container is
# merely up: the server pings Postgres once on start and gives up if nothing
# is answering yet.
db:
	docker compose up -d --wait

test:
	go test ./...

build: front
	go build -o bin/racketka ./cmd/server

front:
	cd web && npm install && npm run build

dev: db
	go run ./cmd/server

telegram:
	./scripts/telegram.sh

tidy:
	go mod tidy && gofmt -w .
