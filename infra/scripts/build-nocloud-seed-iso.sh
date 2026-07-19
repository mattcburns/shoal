#!/usr/bin/env bash
# build-nocloud-seed-iso.sh — tiny CIDATA/NoCloud seed ISO for offline cloud-init.
#
# Offline only: guest must not fetch user-data over the network. This ISO is
# attached as secondary Virtual Media (seed_delivery=second_media) or used as
# input for other offline seed paths.
#
# Usage:
#   SHOAL_SEED_HOSTNAME=node1 ./infra/scripts/build-nocloud-seed-iso.sh [/out/seed.iso]
#   SHOAL_SEED_USER_DATA=/path/to/user-data SHOAL_SEED_META_DATA=/path/to/meta-data \
#     ./infra/scripts/build-nocloud-seed-iso.sh /srv/iso/cidata.iso
set -euo pipefail

OUT="${1:-${SHOAL_SEED_ISO_OUT:-./shoal-nocloud-seed.iso}}"
HOSTNAME="${SHOAL_SEED_HOSTNAME:-${SHOAL_AUTOINSTALL_HOSTNAME:-shoal-node}}"
INSTANCE_ID="${SHOAL_SEED_INSTANCE_ID:-shoal-${HOSTNAME}}"
USERNAME="${SHOAL_SEED_USERNAME:-${SHOAL_AUTOINSTALL_USERNAME:-ubuntu}}"
# Lab-only optional password for cloud-config (empty = lock_passwd / no chpasswd).
PASSWORD="${SHOAL_SEED_PASSWORD:-${SHOAL_AUTOINSTALL_PASSWORD:-}}"

if ! command -v xorriso >/dev/null 2>&1 && ! command -v genisoimage >/dev/null 2>&1 && ! command -v mkisofs >/dev/null 2>&1; then
  echo "error: need xorriso, genisoimage, or mkisofs" >&2
  exit 1
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/shoal-nocloud-seed.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

SEED="$WORK/cidata"
mkdir -p "$SEED"

if [[ -n "${SHOAL_SEED_META_DATA:-}" && -r "${SHOAL_SEED_META_DATA}" ]]; then
  cp "${SHOAL_SEED_META_DATA}" "$SEED/meta-data"
else
  cat >"$SEED/meta-data" <<EOF
instance-id: ${INSTANCE_ID}
local-hostname: ${HOSTNAME}
EOF
fi

if [[ -n "${SHOAL_SEED_USER_DATA:-}" && -r "${SHOAL_SEED_USER_DATA}" ]]; then
  cp "${SHOAL_SEED_USER_DATA}" "$SEED/user-data"
else
  if [[ -n "$PASSWORD" ]]; then
    cat >"$SEED/user-data" <<EOF
#cloud-config
hostname: ${HOSTNAME}
manage_etc_hosts: true
users:
  - name: ${USERNAME}
    lock_passwd: false
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
ssh_pwauth: true
chpasswd:
  expire: false
  list: |
    ${USERNAME}:${PASSWORD}
EOF
  else
    cat >"$SEED/user-data" <<EOF
#cloud-config
hostname: ${HOSTNAME}
manage_etc_hosts: true
users:
  - name: ${USERNAME}
    lock_passwd: true
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
ssh_pwauth: false
EOF
  fi
fi

# Empty network-config keeps some cloud-init versions happy offline.
if [[ ! -f "$SEED/network-config" ]]; then
  printf 'version: 2\n' >"$SEED/network-config"
fi

mkdir -p "$(dirname "$OUT")"
if command -v xorriso >/dev/null 2>&1; then
  xorriso -as mkisofs -quiet -V cidata -o "$OUT" -R -J "$SEED"
elif command -v genisoimage >/dev/null 2>&1; then
  genisoimage -quiet -V cidata -o "$OUT" -R -J "$SEED"
else
  mkisofs -quiet -V cidata -o "$OUT" -R -J "$SEED"
fi
chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c <"$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) NoCloud label=cidata hostname=${HOSTNAME}"
echo "use as seed_iso_url / SHOAL_SEED_ISO_URL for seed_delivery=second_media"
