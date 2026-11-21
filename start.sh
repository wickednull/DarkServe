#!/usr/bin/env bash
# DarkServe launcher (idempotent): starts the server in the background and
# waits until it answers on its port.
#
# The server is started detached (it keeps running after this script ends)
# and writes its process id to data/darkserve.pid, so stop.sh knows which
# process to stop.
set -u
cd "$(dirname "$0")"

PORT="${DARKSERVE_PORT:-7676}"
DATA_DIR="$(pwd)/data"
CACHE_DIR="$(pwd)/cache"
CONF="$DATA_DIR/darkserve.conf"
PID_FILE="$DATA_DIR/darkserve.pid"

mkdir -p "$DATA_DIR" "$CACHE_DIR"

# Already up? Then there is nothing to do. The port check uses bash itself,
# so it works without lsof or curl installed. The descriptor is a high one,
# so it cannot clash with anything the AppImage runtime holds open.
if (exec 9<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
  echo "DarkServe already running → http://localhost:$PORT"
  exit 0
fi

cat > "$CONF" <<EOF
addr: ":$PORT"
data_dir: "$DATA_DIR"
cache_dir: "$CACHE_DIR"
EOF

# The server prints its own "DarkServe up → …" line, and the tail of the log
# if it fails to start.
./darkserve -config "$CONF" -addr ":$PORT" -daemon -pidfile "$PID_FILE"
exit $?
