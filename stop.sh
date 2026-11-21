#!/usr/bin/env bash
# Stops the DarkServe instance on $PORT (default 7676). Only the process
# that DarkServe itself recorded is stopped, never another app.
set -u
cd "$(dirname "$0")"

PORT="${DARKSERVE_PORT:-7676}"
PID_FILE="$(pwd)/data/darkserve.pid"

if [[ ! -f "$PID_FILE" ]]; then
  echo "port $PORT free (no DarkServe running)"
  exit 0
fi

PID="$(tr -dc '0-9' < "$PID_FILE" 2>/dev/null)"
if [[ -z "$PID" || ! -d "/proc/$PID" ]]; then
  rm -f "$PID_FILE"
  echo "port $PORT free (removed a stale pid file)"
  exit 0
fi

kill "$PID" 2>/dev/null
for _ in $(seq 1 50); do
  [[ -d "/proc/$PID" ]] || break
  sleep 0.2
done

if [[ -d "/proc/$PID" ]]; then
  echo "DarkServe did not stop — run: kill -9 $PID"
  exit 1
fi

rm -f "$PID_FILE"
echo "port $PORT freed"
