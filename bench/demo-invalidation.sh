#!/usr/bin/env bash
# Демо: два экземпляра API на разных портах, общая БД. Админ меняет данные через первый,
# второй применяет изменение сразу (LISTEN/NOTIFY), а не по истечении TTL кэшей
# (AUTH_CACHE_TTL 45 с, CACHE_TTL 10 с).
#
#   task db:up && bench/demo-invalidation.sh
#
# Создаёт временную БД iptv_demo на сервере из ADMIN_DB_URL и удаляет её в конце.
# Основные данные не затрагиваются. Нужны go, curl, psql в PATH или docker с контейнером postgres.
set -euo pipefail

PG_PORT=${HOST_POSTGRES_PORT:-5482}
ADMIN_DB_URL=${ADMIN_DB_URL:-postgres://iptv:iptv@localhost:$PG_PORT/iptv?sslmode=disable}
DEMO_DB_URL=$(printf %s "$ADMIN_DB_URL" | sed 's#/iptv?#/iptv_demo?#')
A=http://127.0.0.1:18091
B=http://127.0.0.1:18092
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
pids=()

psql_admin() {
  if command -v psql >/dev/null; then psql "$ADMIN_DB_URL" -qc "$1"
  else docker exec "$(docker ps -qf name=postgres | head -1)" psql -U iptv -d iptv -qc "$1"; fi
}
cleanup() {
  for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  psql_admin "DROP DATABASE IF EXISTS iptv_demo WITH (FORCE)" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

psql_admin "DROP DATABASE IF EXISTS iptv_demo WITH (FORCE)" >/dev/null
psql_admin "CREATE DATABASE iptv_demo"
(cd "$root" && go build -o "$tmp/server" ./cmd/server)

start() { # адрес
  APP_ENV=dev LOG_LEVEL=warn HTTP_ADDR=":$1" DATABASE_URL="$DEMO_DB_URL" "$tmp/server" >"$tmp/$1.log" 2>&1 &
  pids+=($!)
}
start 18091
start 18092
for url in $A $B; do
  ok=
  for _ in $(seq 300); do curl -fs "$url/healthz" >/dev/null 2>&1 && { ok=1; break; }; sleep 0.1; done
  [ -n "$ok" ] || { echo "$url не поднялся:"; cat "$tmp"/*.log; exit 1; }
done

adm() { curl -fsS -u admin:admin123 -H 'Content-Type: application/json' "$@"; }
id_of() { sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1; }
available() { curl -s -u demo:demo-pass-1 "$B/api/v1/channels" | { grep -o '"available":true' || true; } | wc -l | tr -d ' '; }
status() { curl -s -o /dev/null -w '%{http_code}' -u demo:demo-pass-1 "$B/api/v1/channels"; }
# Ждёт, пока команда $2 выведет значение $1, и печатает, сколько это заняло.
now_ms() { perl -MTime::HiRes=time -e 'printf "%d", time*1000'; }
wait_for() {
  local want=$1; shift
  local start got
  start=$(now_ms)
  for _ in $(seq 200); do
    got=$("$@")
    [ "$got" = "$want" ] && { echo "  -> $got через $(( $(now_ms) - start )) мс"; return 0; }
    sleep 0.01
  done
  echo "  -> НЕ дошло за 2 с, последнее значение: $got"; return 1
}

echo "1. Админ через экземпляр A создаёт пользователя demo с подпиской на пакет 1"
uid=$(adm -X POST "$A/api/v1/admin/users" -d '{"login":"demo","password":"demo-pass-1"}' | id_of)
sid=$(adm -X POST "$A/api/v1/admin/users/$uid/subscriptions" -d '{"package_id":1}' | id_of)

echo "2. demo читает каналы с экземпляра B (кэш пароля и каталога на B заполняется)"
echo "  доступных каналов: $(available), HTTP $(status)"

echo "3. Админ через A выключает подписку; смотрим B без ожидания TTL (10 с)"
adm -X PATCH "$A/api/v1/admin/subscriptions/$sid" -d '{"is_active":false}' >/dev/null
wait_for 0 available

echo "4. Админ через A выключает пользователя; смотрим B без ожидания TTL (45 с)"
adm -X PATCH "$A/api/v1/admin/users/$uid" -d '{"is_active":false}' >/dev/null
wait_for 401 status
