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

# DEV_MODE is set here rather than in .env so the file can stay production-
# shaped: godotenv never overrides what is already in the environment, so this
# wins. It skips the Telegram signature check and signs you in as a test
# player, which is what opens the Mini App in a plain browser. `make telegram`
# is the target that runs it for real.
dev: db
	DEV_MODE=true go run ./cmd/server

telegram:
	./scripts/telegram.sh

tidy:
	go mod tidy && gofmt -w .
