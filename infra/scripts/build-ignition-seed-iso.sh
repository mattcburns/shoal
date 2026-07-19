#!/usr/bin/env bash
# build-ignition-seed-iso.sh — tiny Ignition seed ISO for offline Flatcar (M4).
#
# Offline only: guest must not fetch Ignition over the network
# (no ignition.config.url=http://…). Attach as secondary Virtual Media
# (seed_delivery=second_media) alongside the Flatcar installer ISO.
#
# Layout (on ISO root):
#   config.ign
#   ignition/config.ign   (same content; alternate discovery path)
#
# Usage:
#   SHOAL_IGNITION_FILE=/path/to/config.ign \
#     ./infra/scripts/build-ignition-seed-iso.sh /srv/iso/flatcar-ignition.iso
#   SHOAL_SEED_HOSTNAME=node1 SHOAL_SSH_AUTHORIZED_KEY='ssh-ed25519 AAAA…' \
#     ./infra/scripts/build-ignition-seed-iso.sh ./flatcar-ignition.iso
set -euo pipefail

OUT="${1:-${SHOAL_IGNITION_ISO_OUT:-./shoal-ignition-seed.iso}}"
HOSTNAME="${SHOAL_SEED_HOSTNAME:-${SHOAL_AUTOINSTALL_HOSTNAME:-shoal-node}}"

if ! command -v xorriso >/dev/null 2>&1 && ! command -v genisoimage >/dev/null 2>&1 && ! command -v mkisofs >/dev/null 2>&1; then
  echo "error: need xorriso, genisoimage, or mkisofs" >&2
  exit 1
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/shoal-ignition-seed.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

SEED="$WORK/seed"
mkdir -p "$SEED/ignition"

if [[ -n "${SHOAL_IGNITION_FILE:-}" && -r "${SHOAL_IGNITION_FILE}" ]]; then
  # Reject guest HTTP Ignition URLs in operator-supplied config (offline constraint).
  if grep -Eiq 'ignition\.config\.url["'\'']?\s*[:=]\s*["'\'']?https?://' "${SHOAL_IGNITION_FILE}" 2>/dev/null; then
    echo "error: ignition file must not use ignition.config.url=http(s):// (offline only)" >&2
    exit 1
  fi
  if grep -Eiq '"source"\s*:\s*"https?://' "${SHOAL_IGNITION_FILE}" 2>/dev/null; then
    # Allow only if not clearly a config URL; still warn — operators may fetch packages.
    :
  fi
  cp "${SHOAL_IGNITION_FILE}" "$SEED/config.ign"
else
  # Minimal Ignition 3.x lab template (hostname via files; optional SSH key).
  # Prefer SHOAL_IGNITION_FILE for real fleets — do not log secrets.
  SSH_KEY="${SHOAL_SSH_AUTHORIZED_KEY:-}"
  if [[ -n "$SSH_KEY" ]]; then
    # Escape for JSON string
    SSH_JSON=$(printf '%s' "$SSH_KEY" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().rstrip("\n")))' 2>/dev/null \
      || printf '"%s"' "${SSH_KEY//\\/\\\\}")
    cat >"$SEED/config.ign" <<EOF
{
  "ignition": { "version": "3.3.0" },
  "passwd": {
    "users": [
      {
        "name": "core",
        "sshAuthorizedKeys": [ ${SSH_JSON} ]
      }
    ]
  },
  "storage": {
    "files": [
      {
        "path": "/etc/hostname",
        "mode": 420,
        "overwrite": true,
        "contents": { "source": "data:,${HOSTNAME}" }
      }
    ]
  }
}
EOF
  else
    cat >"$SEED/config.ign" <<EOF
{
  "ignition": { "version": "3.3.0" },
  "storage": {
    "files": [
      {
        "path": "/etc/hostname",
        "mode": 420,
        "overwrite": true,
        "contents": { "source": "data:,${HOSTNAME}" }
      }
    ]
  }
}
EOF
  fi
fi

cp "$SEED/config.ign" "$SEED/ignition/config.ign"

mkdir -p "$(dirname "$OUT")"
if command -v xorriso >/dev/null 2>&1; then
  xorriso -as mkisofs -quiet -V ignition -o "$OUT" -R -J "$SEED"
elif command -v genisoimage >/dev/null 2>&1; then
  genisoimage -quiet -V ignition -o "$OUT" -R -J "$SEED"
else
  mkisofs -quiet -V ignition -o "$OUT" -R -J "$SEED"
fi
chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c <"$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) Ignition seed label=ignition hostname=${HOSTNAME}"
echo "use as seed_iso_url / SHOAL_SEED_ISO_URL for seed_delivery=second_media (os_family=flatcar)"
