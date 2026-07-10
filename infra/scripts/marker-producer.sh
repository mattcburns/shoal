#!/usr/bin/env bash
# marker-producer.sh — emit SHOAL|… protocol lines on stdout (serial console simulation).
#
# Used for:
#   - unit/integration harnesses that feed a PTY or pipe into Observe
#   - a minimal live-image boot script (console=ttyS0,115200n8) that sources this logic
#
# Protocol (design §4.3):
#   SHOAL|<schema_ver>|<seq>|<iso8601_utc>|<phase>|<percent>|<state>|<detail>
#
# Usage:
#   ./marker-producer.sh              # full BOOT → DONE sequence with heartbeats
#   ./marker-producer.sh --loop       # heartbeats only (stall tests: kill the process)
#   ./marker-producer.sh --error      # end with ERROR

set -euo pipefail

INTERVAL="${SHOAL_MARKER_INTERVAL:-1}"
MODE="full"
for arg in "$@"; do
  case "$arg" in
    --loop) MODE="loop" ;;
    --error) MODE="error" ;;
  esac
done

seq=0
emit() {
  local phase="$1" percent="$2" state="$3" detail="${4:-}"
  seq=$((seq + 1))
  local ts
  ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  printf 'SHOAL|1|%s|%s|%s|%s|%s|%s\n' "$seq" "$ts" "$phase" "$percent" "$state" "$detail"
}

heartbeat() {
  local phase="$1"
  emit "$phase" "-" "HEARTBEAT" ""
}

case "$MODE" in
  loop)
    while true; do
      heartbeat "WAIT"
      sleep "$INTERVAL"
    done
    ;;
  error)
    emit "BOOT" "0" "OK" "starting"
    sleep "$INTERVAL"
    emit "IMAGE_WRITE" "20" "OK" "writing"
    sleep "$INTERVAL"
    emit "IMAGE_WRITE" "-" "ERROR" "simulated failure"
    ;;
  full|*)
    emit "BOOT" "0" "OK" "live image started"
    sleep "$INTERVAL"
    heartbeat "BOOT"
    sleep "$INTERVAL"
    emit "IMAGE_WRITE" "25" "OK" "writing rootfs"
    sleep "$INTERVAL"
    heartbeat "IMAGE_WRITE"
    sleep "$INTERVAL"
    emit "IMAGE_WRITE" "75" "OK" "syncing"
    sleep "$INTERVAL"
    emit "VERIFY" "90" "OK" "checksum ok"
    sleep "$INTERVAL"
    emit "DONE" "100" "OK" "reboot pending"
    ;;
esac
