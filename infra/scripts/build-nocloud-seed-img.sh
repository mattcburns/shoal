#!/usr/bin/env bash
# build-nocloud-seed-img.sh — raw FAT image (LABEL=cidata) for offline NoCloud seed.
#
# Used by prep config_drive path: prep live dd's this image to the end of the
# install disk so cloud-init can find NoCloud without a second Virtual Media.
#
# Usage:
#   SHOAL_SEED_HOSTNAME=node1 ./infra/scripts/build-nocloud-seed-img.sh [/out/seed.img]
#   SHOAL_SEED_USER_DATA=... SHOAL_SEED_META_DATA=... ./infra/scripts/build-nocloud-seed-img.sh
set -euo pipefail

OUT="${1:-${SHOAL_SEED_IMG_OUT:-./shoal-nocloud-seed.img}}"
HOSTNAME="${SHOAL_SEED_HOSTNAME:-${SHOAL_AUTOINSTALL_HOSTNAME:-shoal-node}}"
INSTANCE_ID="${SHOAL_SEED_INSTANCE_ID:-shoal-${HOSTNAME}}"
USERNAME="${SHOAL_SEED_USERNAME:-${SHOAL_AUTOINSTALL_USERNAME:-ubuntu}}"
PASSWORD="${SHOAL_SEED_PASSWORD:-${SHOAL_AUTOINSTALL_PASSWORD:-}}"
# Size in MiB (prep reserves this many MiB at end of disk).
SIZE_MIB="${SHOAL_SEED_IMG_SIZE_MIB:-16}"

if ! command -v mkfs.vfat >/dev/null 2>&1; then
  echo "error: need mkfs.vfat (dosfstools)" >&2
  exit 1
fi
if ! command -v mcopy >/dev/null 2>&1; then
  echo "error: need mcopy (mtools)" >&2
  exit 1
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/shoal-nocloud-seed-img.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

TREE="$WORK/tree"
mkdir -p "$TREE"

if [[ -n "${SHOAL_SEED_META_DATA:-}" && -r "${SHOAL_SEED_META_DATA}" ]]; then
  cp "${SHOAL_SEED_META_DATA}" "$TREE/meta-data"
else
  cat >"$TREE/meta-data" <<EOF
instance-id: ${INSTANCE_ID}
local-hostname: ${HOSTNAME}
EOF
fi

if [[ -n "${SHOAL_SEED_USER_DATA:-}" && -r "${SHOAL_SEED_USER_DATA}" ]]; then
  cp "${SHOAL_SEED_USER_DATA}" "$TREE/user-data"
else
  if [[ -n "$PASSWORD" ]]; then
    cat >"$TREE/user-data" <<EOF
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
    cat >"$TREE/user-data" <<EOF
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
printf 'version: 2\n' >"$TREE/network-config"

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
dd if=/dev/zero of="$OUT" bs=1M count="$SIZE_MIB" status=none
# Uppercase volume label is more portable; cloud-init matches cidata/CIDATA.
mkfs.vfat -n CIDATA -F 16 "$OUT" >/dev/null
# mtools: map drive as image
export MTOOLS_SKIP_CHECK=1
mcopy -i "$OUT" "$TREE/meta-data" ::meta-data
mcopy -i "$OUT" "$TREE/user-data" ::user-data
mcopy -i "$OUT" "$TREE/network-config" ::network-config
chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c <"$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) FAT label=cidata hostname=${HOSTNAME}"
echo "embed with SHOAL_SEED_IMG=$OUT when building prep ISO (config_drive)"
