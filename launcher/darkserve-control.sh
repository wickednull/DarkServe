#!/usr/bin/env bash
# DarkServe control panel — start, open, stop or kill the server from one window.
# Uses yad when a display is available, and a plain menu otherwise.
set -u

PORT="${DARKSERVE_PORT:-7676}"
URL="http://localhost:${PORT}"

# Resolve the project directory so the panel works from any cwd.
SELF="$(readlink -f "$0")"
ROOT="$(dirname "$(dirname "$SELF")")"
BIN="$ROOT/darkserve"
START="$ROOT/start.sh"
STOP="$ROOT/stop.sh"
PID_FILE="$ROOT/data/darkserve.pid"
LOG="$ROOT/data/run.log"

# The pid file is written by the server itself, so the panel only ever stops
# DarkServe — never another app that happens to sit on the port.
running_pids() {
  [[ -f "$PID_FILE" ]] || return 0
  local pid
  pid="$(tr -dc '0-9' < "$PID_FILE" 2>/dev/null)"
  if [[ -n "$pid" && -d "/proc/$pid" ]]; then
    echo "$pid"
  fi
}

status_text() {
  local pids
  pids="$(running_pids)"
  if [[ -n "$pids" ]]; then
    echo "RUNNING — ${URL}"
  else
    echo "STOPPED"
  fi
}

do_start() {
  if [[ -n "$(running_pids)" ]]; then
    echo "Already running on ${URL}"
    return 0
  fi
  if [[ ! -x "$BIN" ]]; then
    echo "Server binary not found at ${BIN} — build it first:"
    echo "  cd ${ROOT} && go build -o darkserve ./cmd"
    return 1
  fi
  "$START" 2>&1
}

do_open() {
  if [[ -z "$(running_pids)" ]]; then
    echo "Not running yet — start the server first."
    return 1
  fi
  if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$URL" >/dev/null 2>&1 &
  elif command -v firefox >/dev/null 2>&1; then
    firefox "$URL" >/dev/null 2>&1 &
  else
    echo "No browser opener found. Open ${URL} manually."
    return 1
  fi
  echo "Opened ${URL}"
}

do_stop() {
  if [[ -z "$(running_pids)" ]]; then
    echo "Nothing to stop."
    return 0
  fi
  "$STOP" 2>&1
}

# KILL force-stops DarkServe only, never other apps on the port.
do_kill() {
  local mine
  mine="$(running_pids)"
  if [[ -z "$mine" ]]; then
    echo "No DarkServe process found."
    return 0
  fi
  kill -9 $mine 2>/dev/null
  rm -f "$PID_FILE"
  sleep 1
  echo "Killed DarkServe (pid ${mine//$'\n'/, })"
}

show_log() {
  [[ -f "$LOG" ]] || { echo "No log yet."; return 0; }
  tail -n 200 "$LOG"
}

# KDE's session manager (ksmserver) files every window it cannot manage itself as a
# "legacy" app, remembering the window's WM_COMMAND, and re-runs that command at the
# next login. yad's WM_COMMAND is just "yad", so leaving this panel open when you log
# out makes a bare, argument-less yad dialog pop up on the next boot. Tagging the
# window with an SM_CLIENT_ID property makes ksmserver skip it, so the panel is never
# saved or restored. Harmless (and skipped) on desktops without xdotool/xprop.
mark_no_restore() {
  local pid="$1" w
  command -v xdotool >/dev/null 2>&1 || return 0
  command -v xprop >/dev/null 2>&1 || return 0
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    w="$(xdotool search --pid "$pid" 2>/dev/null)"
    [[ -n "$w" ]] && break
    sleep 0.2
  done
  [[ -n "$w" ]] || return 0
  while read -r id; do
    [[ -n "$id" ]] || continue
    xprop -id "$id" -f SM_CLIENT_ID 8s -set SM_CLIENT_ID "darkserve-no-restore" 2>/dev/null || true
  done <<< "$w"
}

# Run yad in the background so its window can be tagged, then wait for it, so the
# caller still sees yad's exit code. stdin/stdout stay inherited (the log viewer
# reads the log through a pipe).
run_panel() {
  local pid
  yad "$@" &
  pid=$!
  mark_no_restore "$pid"
  wait "$pid"
}

gui_loop() {
  local msg code
  while true; do
    msg="Status: $(status_text)
Port: ${PORT}   URL: ${URL}
Data: ${ROOT}/data"
    run_panel --title="DarkServe — Media Server" \
        --window-icon="applications-internet" \
        --width=470 --height=200 --center --on-top \
        --text="$msg" --text-align=center \
        --button="START SERVER:0" \
        --button="GO TO SERVER:1" \
        --button="STOP:2" \
        --button="KILL:3" \
        --button="LOG:4" \
        --button="QUIT:5" 2>/dev/null
    code=$?
    case "$code" in
      0) msg="$(do_start)" ;;
      1) msg="$(do_open)" ;;
      2) msg="$(do_stop)" ;;
      3) msg="$(do_kill)" ;;
      4) show_log | run_panel --title="DarkServe log" --text-info --width=760 --height=420 2>/dev/null; msg="Log shown." ;;
      5|252|1) exit 0 ;;
    esac
    if [[ $code -eq 0 || $code -eq 1 || $code -eq 2 || $code -eq 3 ]]; then
      run_panel --info --title="DarkServe" --text="$msg" --width=380 --no-buttons --timeout=3 2>/dev/null || true
    fi
  done
}

# Text menu: used headless, or when yad is not installed.
menu_loop() {
  while true; do
    echo ""
    echo "DarkServe — $(status_text)  (${URL})"
    echo "  1) START SERVER"
    echo "  2) GO TO SERVER (open browser)"
    echo "  3) STOP"
    echo "  4) KILL (force)"
    echo "  5) VIEW LOG"
    echo "  q) QUIT"
    printf "choose: "
    local c
    read -r c
    case "$c" in
      1) do_start ;;
      2) do_open ;;
      3) do_stop ;;
      4) do_kill ;;
      5) show_log | tail -n 40 ;;
      q|Q) exit 0 ;;
      *) echo "unknown option" ;;
    esac
  done
}

if [[ "${1:-}" == "--cli" ]]; then
  # One-shot actions for scripts: --cli start|open|stop|kill|status|log
  case "${2:-status}" in
    start) do_start ;;
    open)  do_open ;;
    stop)  do_stop ;;
    kill)  do_kill ;;
    status) status_text ;;
    log)   show_log ;;
    *) echo "usage: $0 --cli start|open|stop|kill|status|log" ;;
  esac
  exit $?
fi

if command -v yad >/dev/null 2>&1 && [[ -n "${DISPLAY:-}" || -n "${WAYLAND_DISPLAY:-}" ]]; then
  gui_loop
else
  menu_loop
fi
