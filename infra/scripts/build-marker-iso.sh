#!/usr/bin/env bash
# build-marker-iso.sh — build a minimal bootable live ISO that emits SHOAL| markers
# on the serial console (ttyS0 / console), then powers off.
#
# Modes (Phase 6a):
#   SHOAL_INSTALL_MODE=simulate (default) — Phase 2 demo markers; optional /payload size note
#   SHOAL_INSTALL_MODE=write — when /payload is present, write it to a target with real progress
#
# Target selection for write mode (first match wins):
#   1) kernel cmdline shoal.target=PATH
#   2) SHOAL_INSTALL_TARGET at build time (baked into init)
#   3) first of /dev/vda /dev/sda if block devices
#   4) /tmp/shoal-install.out (lab harness fallback when no disk)
#
# Primary: run on the lab VM (L1) so the ISO lands in /srv/iso for nginx :8080.
# Alternate: run on a workstation and scp the artifact into the lab ISO dir.
#
# Requirements: busybox (static preferred), cpio, gzip, xorriso, isolinux/syslinux.
#
# Usage:
#   ./infra/scripts/build-marker-iso.sh [/output/path/shoal-marker.iso]
#   SHOAL_ISO_OUT=/srv/iso/shoal-marker.iso ./infra/scripts/build-marker-iso.sh
#   SHOAL_INSTALL_MODE=write SHOAL_PAYLOAD_FILE=./rootfs.img ./infra/scripts/build-marker-iso.sh

set -euo pipefail

OUT="${1:-${SHOAL_ISO_OUT:-./shoal-marker.iso}}"
# Optional basename override (Phase 5c Go builder sets SHOAL_ISO_NAME).
if [[ -n "${SHOAL_ISO_NAME:-}" ]]; then
  OUT_DIR="$(dirname "$OUT")"
  OUT="${OUT_DIR%/}/${SHOAL_ISO_NAME}"
fi
WORK="$(mktemp -d "${TMPDIR:-/tmp}/shoal-marker-iso.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

KERNEL="${SHOAL_KERNEL:-}"
if [[ -z "$KERNEL" ]]; then
  for k in /boot/vmlinuz /boot/vmlinuz-linux /boot/vmlinuz-generic; do
    if [[ -r "$k" ]]; then KERNEL="$k"; break; fi
  done
  if [[ -z "$KERNEL" ]]; then
    # Prefer versioned kernels
    KERNEL="$(ls -1 /boot/vmlinuz-* 2>/dev/null | tail -1 || true)"
  fi
fi
if [[ -z "$KERNEL" || ! -r "$KERNEL" ]]; then
  echo "error: no readable kernel; set SHOAL_KERNEL=/boot/vmlinuz-..." >&2
  exit 1
fi

BUSYBOX="$(command -v busybox || true)"
if [[ -z "$BUSYBOX" ]]; then
  echo "error: busybox not found" >&2
  exit 1
fi
if [[ -x /bin/busybox ]]; then
  BUSYBOX=/bin/busybox
fi

ISOLINUX_BIN=""
for p in \
  /usr/lib/ISOLINUX/isolinux.bin \
  /usr/lib/syslinux/isolinux.bin \
  /usr/share/syslinux/isolinux.bin
do
  if [[ -r "$p" ]]; then ISOLINUX_BIN="$p"; break; fi
done
LDLINUX=""
for p in \
  /usr/lib/syslinux/modules/bios/ldlinux.c32 \
  /usr/lib/ISOLINUX/ldlinux.c32 \
  /usr/share/syslinux/ldlinux.c32
do
  if [[ -r "$p" ]]; then LDLINUX="$p"; break; fi
done
if [[ -z "$ISOLINUX_BIN" ]]; then
  echo "error: isolinux.bin not found (install isolinux / syslinux-common)" >&2
  exit 1
fi
if ! command -v xorriso >/dev/null 2>&1; then
  echo "error: xorriso required" >&2
  exit 1
fi

INSTALL_MODE="${SHOAL_INSTALL_MODE:-simulate}"
case "$INSTALL_MODE" in
  simulate|write) ;;
  *)
    echo "error: SHOAL_INSTALL_MODE must be simulate or write (got $INSTALL_MODE)" >&2
    exit 1
    ;;
esac
# Baked default target for write mode (can be overridden by kernel cmdline shoal.target=).
INSTALL_TARGET_DEFAULT="${SHOAL_INSTALL_TARGET:-}"

echo "kernel:  $KERNEL"
echo "busybox: $BUSYBOX"
echo "mode:    $INSTALL_MODE"
echo "output:  $OUT"

# --- initramfs rootfs ---
ROOT="$WORK/rootfs"
mkdir -p "$ROOT"/{bin,dev,proc,sys,tmp}

cp "$BUSYBOX" "$ROOT/bin/busybox"
chmod 755 "$ROOT/bin/busybox"
# Install common applets used by /init
for app in sh mount umount mkdir sleep cat echo poweroff reboot mkdir ln dd wc rm sync; do
  ln -sf busybox "$ROOT/bin/$app"
done

# Optional payload (Phase 5c text / Phase 6a binary image).
# Prefer SHOAL_PAYLOAD_FILE (path); else SHOAL_EMBEDDED_PAYLOAD (inline text).
if [[ -n "${SHOAL_PAYLOAD_FILE:-}" && -r "${SHOAL_PAYLOAD_FILE}" ]]; then
  cp "${SHOAL_PAYLOAD_FILE}" "$ROOT/payload"
  chmod 644 "$ROOT/payload"
elif [[ -n "${SHOAL_EMBEDDED_PAYLOAD:-}" ]]; then
  printf '%s' "${SHOAL_EMBEDDED_PAYLOAD}" > "$ROOT/payload"
  chmod 644 "$ROOT/payload"
fi

# Bake install defaults for init (read by the shell script below).
printf '%s\n' "$INSTALL_MODE" > "$ROOT/install_mode"
printf '%s\n' "$INSTALL_TARGET_DEFAULT" > "$ROOT/install_target_default"

# Init emits markers then powers off. console=ttyS0 from kernel cmdline.
cat > "$ROOT/init" << 'INIT'
#!/bin/busybox sh
export PATH=/bin

/bin/busybox mount -t proc none /proc 2>/dev/null || true
/bin/busybox mount -t sysfs none /sys 2>/dev/null || true
/bin/busybox mount -t devtmpfs none /dev 2>/dev/null || true

# Prefer serial console for markers
for dev in /dev/ttyS0 /dev/console /dev/tty0; do
  if [ -w "$dev" ]; then
    exec >"$dev" 2>&1
    break
  fi
done

seq=0
emit() {
  phase="$1"; percent="$2"; state="$3"; detail="${4:-}"
  seq=$((seq + 1))
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo 1970-01-01T00:00:00Z)"
  printf 'SHOAL|1|%s|%s|%s|%s|%s|%s\n' "$seq" "$ts" "$phase" "$percent" "$state" "$detail"
}

MODE="simulate"
if [ -f /install_mode ]; then
  MODE="$(cat /install_mode 2>/dev/null | tr -d '\n' || echo simulate)"
fi
TARGET=""
if [ -f /install_target_default ]; then
  TARGET="$(cat /install_target_default 2>/dev/null | tr -d '\n' || true)"
fi
# Kernel cmdline overrides (shoal.mode= write|simulate, shoal.target=/dev/vda)
for x in $(cat /proc/cmdline 2>/dev/null); do
  case "$x" in
    shoal.mode=*) MODE="${x#shoal.mode=}" ;;
    shoal.target=*) TARGET="${x#shoal.target=}" ;;
  esac
done

boot_detail="marker live image started mode=${MODE}"
if [ -f /payload ]; then
  psz="$(wc -c </payload 2>/dev/null || echo 0)"
  boot_detail="marker live image started mode=${MODE} payload_bytes=${psz}"
fi
emit BOOT 0 OK "$boot_detail"
sleep 1
emit BOOT - HEARTBEAT ""

if [ "$MODE" = "write" ] && [ -f /payload ]; then
  # Resolve write target.
  if [ -z "$TARGET" ]; then
    for cand in /dev/vda /dev/sda /dev/nvme0n1; do
      if [ -b "$cand" ]; then TARGET="$cand"; break; fi
    done
  fi
  if [ -z "$TARGET" ]; then
    TARGET="/tmp/shoal-install.out"
    emit IMAGE_WRITE 0 OK "no block device; using file target ${TARGET}"
  fi

  total="$(wc -c </payload 2>/dev/null || echo 0)"
  if [ -z "$total" ] || [ "$total" = "0" ]; then
    emit ERROR 0 ERROR "empty payload"
    sleep 2
    /bin/busybox poweroff -f 2>/dev/null || true
    while true; do sleep 3600; done
  fi

  emit IMAGE_WRITE 0 OK "write start target=${TARGET} bytes=${total}"
  # Chunked copy for progress (1 MiB blocks).
  bs=1048576
  # shell arithmetic: number of full blocks
  blocks=$(( total / bs ))
  rem=$(( total % bs ))
  written=0
  i=0
  while [ "$i" -lt "$blocks" ]; do
    dd if=/payload of="$TARGET" bs="$bs" count=1 skip="$i" seek="$i" conv=notrunc 2>/dev/null || {
      emit ERROR 0 ERROR "dd failed at block ${i}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    }
    written=$(( written + bs ))
    pct=$(( written * 100 / total ))
    if [ "$pct" -gt 99 ]; then pct=99; fi
    emit IMAGE_WRITE "$pct" OK "wrote ${written}/${total}"
    i=$(( i + 1 ))
  done
  if [ "$rem" -gt 0 ]; then
    dd if=/payload of="$TARGET" bs=1 count="$rem" skip="$written" seek="$written" conv=notrunc 2>/dev/null || {
      emit ERROR 0 ERROR "dd failed on remainder"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    }
    written=$(( written + rem ))
  fi
  sync 2>/dev/null || true
  emit IMAGE_WRITE 100 OK "write complete target=${TARGET}"
  # Verify size on target when it is a regular file.
  if [ -f "$TARGET" ]; then
    tsz="$(wc -c <"$TARGET" 2>/dev/null || echo 0)"
    if [ "$tsz" = "$total" ]; then
      emit VERIFY 100 OK "size match bytes=${total}"
    else
      emit ERROR 0 ERROR "size mismatch got=${tsz} want=${total}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
  else
    emit VERIFY 100 OK "write finished target=${TARGET}"
  fi
  emit DONE 100 OK "install write complete"
else
  # Phase-2 demonstration sequence (no real disk write).
  sleep 1
  emit IMAGE_WRITE 25 OK "simulated write"
  sleep 1
  emit IMAGE_WRITE - HEARTBEAT ""
  sleep 1
  emit IMAGE_WRITE 75 OK "simulated sync"
  sleep 1
  emit VERIFY 90 OK "ok"
  sleep 1
  emit DONE 100 OK "reboot pending"
fi

sleep 1
/bin/busybox poweroff -f 2>/dev/null || /bin/busybox reboot -f 2>/dev/null || true
while true; do sleep 3600; done
INIT
chmod 755 "$ROOT/init"

# Build initrd (newc cpio + gzip)
INITRD="$WORK/initrd.img"
(
  cd "$ROOT"
  find . | cpio -o -H newc --quiet | gzip -9 > "$INITRD"
)

# ISO tree
ISO="$WORK/iso"
mkdir -p "$ISO/boot/isolinux"
cp "$KERNEL" "$ISO/boot/vmlinuz"
cp "$INITRD" "$ISO/boot/initrd.img"
cp "$ISOLINUX_BIN" "$ISO/boot/isolinux/isolinux.bin"
if [[ -n "$LDLINUX" ]]; then
  cp "$LDLINUX" "$ISO/boot/isolinux/ldlinux.c32"
fi

# Pass mode/target on kernel cmdline for runtime override.
CMDLINE="initrd=/boot/initrd.img console=ttyS0,115200n8 console=tty0 quiet shoal.mode=${INSTALL_MODE}"
if [[ -n "$INSTALL_TARGET_DEFAULT" ]]; then
  CMDLINE="${CMDLINE} shoal.target=${INSTALL_TARGET_DEFAULT}"
fi

cat > "$ISO/boot/isolinux/isolinux.cfg" << CFG
DEFAULT shoal
PROMPT 0
TIMEOUT 10
LABEL shoal
  KERNEL /boot/vmlinuz
  APPEND ${CMDLINE}
CFG

VOLID="SHOAL_MARKER"
if [[ "$INSTALL_MODE" = "write" ]]; then
  VOLID="SHOAL_INSTALL"
fi

mkdir -p "$(dirname "$OUT")"
xorriso -as mkisofs \
  -quiet \
  -o "$OUT" \
  -V "$VOLID" \
  -b boot/isolinux/isolinux.bin \
  -c boot/isolinux/boot.cat \
  -no-emul-boot \
  -boot-load-size 4 \
  -boot-info-table \
  "$ISO"

chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c < "$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) mode=$INSTALL_MODE"
echo "publish: copy into lab ISO dir and serve as http://<lab>:8080/$(basename "$OUT")"
echo "BMC-reachable (VM lab nodes): http://192.168.124.1:8080/$(basename "$OUT")"
