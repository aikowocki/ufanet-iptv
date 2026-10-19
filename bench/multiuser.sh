#!/usr/bin/env bash
# Нагрузка от многих пользователей и холодный старт.
#
#   bench/multiuser.sh <label>
#
# Результат: bench/results/<label>/multiuser/{steady,cold}.txt
#   cold   - перезапуск app (пустые кэши), сразу 15 с нагрузки от 200 разных пользователей
#   steady - те же 200 пользователей, но после прогрева (повторный проход)
# Стенд должен быть поднят: task load:up. Параметры: USERS (200), C (50).
set -euo pipefail

label=${1:?usage: bench/multiuser.sh <label>}
USERS=${USERS:-200}
C=${C:-50}
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/bench/results/$label/multiuser"
compose=(docker compose -p iptv-load -f "$root/docker-compose.loadtest.yml")
cd "$root"

curl -fsS http://127.0.0.1:18080/healthz >/dev/null || { echo "стенд не поднят: task load:up" >&2; exit 1; }
rm -rf "$out" && mkdir -p "$out"
go build -o "$out/.multiuser" ./bench/multiuser
trap 'rm -f "$out/.multiuser"' EXIT

"$out/.multiuser" -setup -users "$USERS"

# Холодный старт: свежий процесс, кэши пусты.
"${compose[@]}" restart app >/dev/null
"$out/.multiuser" -users "$USERS" -c "$C" -d 15s -timeline 8 | tee "$out/cold.txt"

echo
"$out/.multiuser" -users "$USERS" -c "$C" -d 15s -timeline 3 | tee "$out/steady.txt"
