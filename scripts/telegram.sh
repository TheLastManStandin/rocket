#!/usr/bin/env bash
# Everything needed to open the Mini App from inside Telegram: database, a
# built front end, the server, and a public HTTPS tunnel. Prints the tunnel
# address, which is what BotFather wants in the Web App URL field, then holds
# the whole lot open until interrupted.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

logs=$(mktemp -d)
server_pid=""
tunnel_pid=""

cleanup() {
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  [ -n "$tunnel_pid" ] && kill "$tunnel_pid" 2>/dev/null || true
  rm -rf "$logs"
}
# Ctrl+C is how this is meant to end, so it exits clean rather than leaving
# make to report a signal as a failure.
trap cleanup EXIT
trap 'exit 0' INT TERM

value_of() {
  grep -E "^$1=.+" .env | head -1 | cut -d= -f2- || true
}

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# ---------- configuration ----------

[ -f .env ] || cp .env.example .env

# A generated secret is fine here, but it has to be a stable one: regenerating
# it on every run would sign out every player on every restart.
if [ -z "$(value_of JWT_SECRET)" ]; then
  grep -v '^JWT_SECRET=' .env > "$logs/env" || true
  printf 'JWT_SECRET=%s\n' "$(random_hex)" >> "$logs/env"
  cp "$logs/env" .env
  echo "→ .env: сгенерирован JWT_SECRET"
fi

if [ -z "$(value_of BOT_TOKEN)" ]; then
  echo "BOT_TOKEN пуст в .env — возьмите токен у @BotFather и впишите его." >&2
  exit 1
fi

if [ "$(value_of DEV_MODE)" = "true" ]; then
  echo "! DEV_MODE=true: подпись Telegram не проверяется, и по ссылке ниже"
  echo "  зайти сможет кто угодно. Для боевой проверки поставьте false."
fi

addr=$(value_of ADDR)
port=${addr##*:}
port=${port:-8090}

# ---------- database ----------

echo "→ Postgres"
docker compose up -d >/dev/null
for _ in $(seq 1 40); do
  docker compose exec -T db pg_isready -U racketka -d racketka >/dev/null 2>&1 && break
  sleep 0.5
done

# ---------- front end ----------

echo "→ сборка фронта"
[ -d web/node_modules ] || (cd web && npm install >"$logs/npm.log" 2>&1)
# Quiet while it works, loud when it doesn't: the address at the end is what
# this script is for, and vite's warnings would bury it.
if ! (cd web && npm run build >"$logs/front.log" 2>&1); then
  cat "$logs/front.log" >&2
  exit 1
fi

# ---------- server ----------

# Built rather than `go run`: killing `go run` leaves its child holding the
# port, which then blocks the next run.
echo "→ сервер на :$port"
go build -o bin/racketka ./cmd/server
./bin/racketka > "$logs/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 40); do
  curl -fsS "http://localhost:$port/api/health" >/dev/null 2>&1 && break
  sleep 0.25
done
if ! curl -fsS "http://localhost:$port/api/health" >/dev/null 2>&1; then
  echo "сервер не поднялся:" >&2
  cat "$logs/server.log" >&2
  exit 1
fi

# ---------- tunnel ----------

echo "→ туннель"
if command -v cloudflared >/dev/null 2>&1; then
  cloudflared tunnel --url "http://localhost:$port" > "$logs/tunnel.log" 2>&1 &
else
  ssh -o StrictHostKeyChecking=accept-new -o ServerAliveInterval=30 \
    -R "80:localhost:$port" nokey@localhost.run > "$logs/tunnel.log" 2>&1 &
fi
tunnel_pid=$!

url=""
for _ in $(seq 1 60); do
  url=$(grep -ohE 'https://[A-Za-z0-9.-]+\.(trycloudflare\.com|lhr\.life)' \
    "$logs/tunnel.log" | head -1 || true)
  [ -n "$url" ] && break
  sleep 0.5
done

echo
if [ -n "$url" ]; then
  echo "Web App URL для BotFather:"
  echo
  echo "    $url"
  echo
  echo "/newapp → выбрать бота (тот же, чей токен в BOT_TOKEN) → вставить адрес."
else
  # The address is the whole point, so if the pattern moved, hand over the raw
  # output rather than failing silently.
  echo "Адрес выцепить не удалось, вот вывод туннеля:" >&2
  cat "$logs/tunnel.log" >&2
fi
echo "Ctrl+C — остановить сервер и туннель."

wait "$server_pid"
