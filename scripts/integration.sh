#!/usr/bin/env bash
set -euo pipefail

# Starts disposable standalone, Sentinel and 3-node Cluster Redis servers,
# then runs the integration-tagged tests. Requires redis-server and
# redis-cli (Redis 6+; tested with 8.x), and Python 3.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REDIS_SERVER="${REDIS_SERVER:-redis-server}"
REDIS_CLI="${REDIS_CLI:-redis-cli}"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/kivrix-it.XXXXXX")"
PIDS=()
cleanup() {
  if [ "${#PIDS[@]}" -gt 0 ]; then
    for pid in "${PIDS[@]}"; do
      kill "$pid" 2>/dev/null || true
    done
  fi
  wait 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

wait_server() {
  local port="$1" pid="$2" server_pid
  for _ in $(seq 1 100); do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "test redis on port $port exited before becoming ready" >&2
      return 1
    fi
    server_pid="$("$REDIS_CLI" -h 127.0.0.1 -p "$port" --raw INFO server 2>/dev/null | tr -d '\r' | awk -F: '$1 == "process_id" {print $2}' || true)"
    if [ "$server_pid" = "$pid" ]; then
      return 0
    fi
    sleep 0.1
  done
  echo "redis on port $port did not start" >&2
  return 1
}

start_server() {
  local port="$1" dir="$2"
  shift 2
  mkdir -p "$dir"
  "$REDIS_SERVER" --bind 127.0.0.1 --port "$port" --dir "$dir" --save '' --appendonly no "$@" \
    >"$dir/server.log" 2>&1 &
  PIDS+=("$!")
  wait_server "$port" "${PIDS[${#PIDS[@]}-1]}"
}

# Check both client and cluster-bus ports without sending commands to other services.
python3 - <<'PY'
import socket
for port in (16379, 16380, 26380, 17000, 17001, 17002, 27000, 27001, 27002):
    with socket.socket() as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind(("127.0.0.1", port))
        except OSError as error:
            raise SystemExit(f"test port {port} is unavailable: {error}") from None
PY

# Standalone, also reachable over a Unix socket.
start_server 16379 "$TMP/standalone" --unixsocket "$TMP/redis.sock" --unixsocketperm 700

# Sentinel master and sentinel.
start_server 16380 "$TMP/sentinel-master"
cat >"$TMP/sentinel.conf" <<CONF
bind 127.0.0.1
port 26380
dir $TMP
sentinel monitor mymaster 127.0.0.1 16380 1
sentinel down-after-milliseconds mymaster 2000
sentinel failover-timeout mymaster 10000
CONF
"$REDIS_SERVER" "$TMP/sentinel.conf" --sentinel >"$TMP/sentinel.log" 2>&1 &
PIDS+=("$!")
wait_server 26380 "${PIDS[${#PIDS[@]}-1]}"

# Three master cluster nodes.
for port in 17000 17001 17002; do
  start_server "$port" "$TMP/cluster-$port" \
    --cluster-enabled yes \
    --cluster-config-file "nodes-$port.conf" \
    --cluster-node-timeout 5000
done
"$REDIS_CLI" --cluster create 127.0.0.1:17000 127.0.0.1:17001 127.0.0.1:17002 \
  --cluster-replicas 0 --cluster-yes >/dev/null
for _ in $(seq 1 100); do
  state="$("$REDIS_CLI" -p 17000 cluster info 2>/dev/null | tr -d '\r' | awk -F: '/cluster_state/ {print $2}')"
  if [ "$state" = "ok" ]; then break; fi
  sleep 0.1
done

cd "$ROOT"
export REDIS_PILOT_TEST_STANDALONE="127.0.0.1:16379"
export REDIS_PILOT_TEST_UNIX="$TMP/redis.sock"
export REDIS_PILOT_TEST_SENTINEL="127.0.0.1:26380"
export REDIS_PILOT_TEST_SENTINEL_MASTER="mymaster"
export REDIS_PILOT_TEST_CLUSTER="127.0.0.1:17000,127.0.0.1:17001,127.0.0.1:17002"
go test -tags=integration -run Integration -count=1 -v ./...
