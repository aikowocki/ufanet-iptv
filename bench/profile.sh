#!/usr/bin/env bash
# Воспроизводимый замер: нагрузка + профили pprof + статистика запросов Postgres.
#
#   bench/profile.sh <label> <scenario>
#
# scenario:
#   channels  GET /api/v1/channels (Basic Auth, горячий путь API)
#   playback  GET /api/v1/channels/1/playback (подписанная ссылка)
#   playlist  GET плейлиста на стрим-сервере по подписанной ссылке
#
# Параметры окружения: C (параллелизм, 50), DURATION (30), CPU_SECONDS (20), WARMUP (5).
# Стенд должен быть поднят: task load:up. Результат: bench/results/<label>/<scenario>/.
set -euo pipefail

label=${1:?usage: bench/profile.sh <label> <scenario>}
scenario=${2:?usage: bench/profile.sh <label> <scenario>}
C=${C:-50}
DURATION=${DURATION:-30}
CPU_SECONDS=${CPU_SECONDS:-20}
WARMUP=${WARMUP:-5}

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/bench/results/$label/$scenario"
compose=(docker compose -p iptv-load -f "$root/docker-compose.loadtest.yml")
api=http://127.0.0.1:18080
streamsrv=http://127.0.0.1:18081
cred=alice:alice123

command -v hey >/dev/null || { echo "нужен hey: brew install hey" >&2; exit 1; }
curl -fsS "$api/healthz" >/dev/null || { echo "стенд не поднят: task load:up" >&2; exit 1; }

case $scenario in
channels) url=$api/api/v1/channels pprof=http://127.0.0.1:6060 ;;
playback) url=$api/api/v1/channels/1/playback pprof=http://127.0.0.1:6060 ;;
playlist)
  url=$(curl -fsS -u "$cred" "$api/api/v1/channels/1/playback" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p' | sed 's/\\u0026/\&/g')
  pprof=http://127.0.0.1:6061
  # Первый запрос запускает ffmpeg канала; ждём, пока плейлист станет доступен.
  for _ in $(seq 30); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "$url")" = 200 ] && break
    sleep 1
  done
  ;;
*) echo "неизвестный сценарий: $scenario" >&2; exit 1 ;;
esac

rm -rf "$out" && mkdir -p "$out"

{
  echo "label:     $label"
  echo "scenario:  $scenario"
  echo "date:      $(date -u +%FT%TZ)"
  echo "git:       $(git -C "$root" rev-parse --short HEAD)$(git -C "$root" diff --quiet || echo -dirty)"
  echo "host:      $(uname -sm), $(sysctl -n machdep.cpu.brand_string 2>/dev/null || true)"
  echo "go:        $(go version)"
  echo "load:      hey -c $C -z ${DURATION}s (closed loop), warmup ${WARMUP}s, cpu profile ${CPU_SECONDS}s"
  echo "limits:    app/stream cpus=2 mem=1g (docker-compose.loadtest.yml)"
} >"$out/meta.txt"

# Сбрасываем счётчики запросов Postgres, чтобы в отчёт попала только эта нагрузка.
"${compose[@]}" exec -T postgres psql -qU iptv -d iptv \
  -c "CREATE EXTENSION IF NOT EXISTS pg_stat_statements" -c "SELECT pg_stat_statements_reset()" >/dev/null

# -a у hey давал 401, поэтому заголовок Authorization задан явно (стрим-серверу он не нужен: там подпись в ссылке).
hey -z "${DURATION}s" -c "$C" -H "Authorization: Basic $(printf %s "$cred" | base64)" "$url" >"$out/hey.txt" &
hey_pid=$!
sleep "$WARMUP"

curl -fsS -o "$out/allocs.pprof" "$pprof/debug/pprof/allocs?seconds=$CPU_SECONDS" &
alloc_pid=$!
curl -fsS -o "$out/cpu.pprof" "$pprof/debug/pprof/profile?seconds=$CPU_SECONDS"
wait "$alloc_pid"
curl -fsS -o "$out/goroutine.txt" "$pprof/debug/pprof/goroutine?debug=1"
"${compose[@]}" ps -q | xargs docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' >"$out/docker-stats.txt"
wait "$hey_pid"

go tool pprof -top -nodecount=25 "$out/cpu.pprof" >"$out/cpu.top.txt" 2>/dev/null
go tool pprof -top -nodecount=25 -sample_index=alloc_space "$out/allocs.pprof" >"$out/allocs.top.txt" 2>/dev/null
"${compose[@]}" exec -T postgres psql -U iptv -d iptv -c \
  "SELECT calls, round(total_exec_time::numeric,1) AS total_ms, round(mean_exec_time::numeric,3) AS mean_ms, left(regexp_replace(query,'\s+',' ','g'),90) AS query
   FROM pg_stat_statements WHERE query NOT ILIKE '%pg_stat_statements%' ORDER BY total_exec_time DESC LIMIT 5" >"$out/pg.txt"

echo "готово: $out"
grep -E "Requests/sec|Average|99%|\[[0-9]+\]" "$out/hey.txt" || true
