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
  # Prefer the running kernel image so packed modules (uname -r) match.
  # /boot/vmlinuz often points at a newer uname-mismatched install and yields
  # "invalid module format" when insmod'ing isofs/ahci from /lib/modules/$(uname -r).
  run_kver="$(uname -r 2>/dev/null || true)"
  if [[ -n "$run_kver" && -r "/boot/vmlinuz-${run_kver}" ]]; then
    KERNEL="/boot/vmlinuz-${run_kver}"
  fi
fi
if [[ -z "$KERNEL" ]]; then
  for k in /boot/vmlinuz /boot/vmlinuz-linux /boot/vmlinuz-generic; do
    if [[ -r "$k" ]]; then KERNEL="$k"; break; fi
  done
  if [[ -z "$KERNEL" ]]; then
    KERNEL="$(ls -1 /boot/vmlinuz-* 2>/dev/null | tail -1 || true)"
  fi
fi
if [[ -z "$KERNEL" || ! -r "$KERNEL" ]]; then
  echo "error: no readable kernel; set SHOAL_KERNEL=/boot/vmlinuz-..." >&2
  exit 1
fi
# Resolve symlinks for logging / version checks.
KERNEL="$(readlink -f "$KERNEL" 2>/dev/null || echo "$KERNEL")"

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
  simulate|write|autoinstall|prep) ;;
  *)
    echo "error: SHOAL_INSTALL_MODE must be simulate|write|autoinstall|prep (got $INSTALL_MODE)" >&2
    exit 1
    ;;
esac
# autoinstall is write-to-disk of an OS image payload (Phase 7a cloud image path).
if [[ "$INSTALL_MODE" == "autoinstall" ]]; then
  INSTALL_MODE="write"
  # Prefer reboot into installed OS after write.
  SHOAL_INSTALL_REBOOT="${SHOAL_INSTALL_REBOOT:-1}"
  # Prefer first block device for real install.
  if [[ -z "${SHOAL_INSTALL_TARGET:-}" ]]; then
    SHOAL_INSTALL_TARGET="/dev/vda"
  fi
fi
# prep: wipe install disk and emit PREP_* markers (multi-stage M2); no OS payload.
if [[ "$INSTALL_MODE" == "prep" ]]; then
  if [[ -z "${SHOAL_INSTALL_TARGET:-}" ]]; then
    SHOAL_INSTALL_TARGET="/dev/vda"
  fi
  # Power off after wipe so orchestrator can attach OS media cleanly.
  SHOAL_INSTALL_REBOOT="${SHOAL_INSTALL_REBOOT:-0}"
fi
# Baked default target for write/prep mode (overridable via kernel cmdline shoal.target=).
INSTALL_TARGET_DEFAULT="${SHOAL_INSTALL_TARGET:-}"
INSTALL_REBOOT="${SHOAL_INSTALL_REBOOT:-0}"
# Wipe style for prep: discard (try blkdiscard) or zero (dd first N MiB of disk).
PREP_WIPE_LEVEL="${SHOAL_PREP_WIPE_LEVEL:-discard}"

echo "kernel:  $KERNEL"
echo "busybox: $BUSYBOX"
echo "mode:    $INSTALL_MODE"
echo "output:  $OUT"

# --- initramfs rootfs ---
ROOT="$WORK/rootfs"
mkdir -p "$ROOT"/{bin,dev,proc,sys,tmp,mnt}

cp "$BUSYBOX" "$ROOT/bin/busybox"
chmod 755 "$ROOT/bin/busybox"
# Install common applets used by /init
for app in sh mount umount mkdir sleep cat echo poweroff reboot mkdir ln dd wc rm sync gunzip zcat gzip \
  insmod modprobe lsmod mdev; do
  ln -sf busybox "$ROOT/bin/$app"
done

# Kernel modules needed when payload lives on the ISO (SATA CD + isofs + virtio disk).
# Host kernel modules: nested lab boots the L1 /boot/vmlinuz; without these, /init
# cannot mount the install CD or see /dev/vda.
pack_modules() {
  local kver
  kver="$(uname -r)"
  local modroot="/lib/modules/${kver}"
  if [[ ! -d "$modroot" ]]; then
    echo "warn: no $modroot — skipping module pack (CD mount may fail)" >&2
    return 0
  fi
  mkdir -p "$ROOT/lib/modules/${kver}"
  # Resolve dependencies for modules we need (ignore builtins / missing).
  local needed=()
  local m dep line
  for m in ahci libahci isofs virtio_blk virtio_pci virtio_ring virtio; do
    if [[ -n "$(modinfo -n "$m" 2>/dev/null || true)" ]]; then
      needed+=("$m")
    fi
  done
  # Collect unique module files via modprobe --show-depends
  local files=()
  for m in "${needed[@]}"; do
    while IFS= read -r line; do
      case "$line" in
        insmod\ *)
          dep="${line#insmod }"
          dep="${dep%% *}"
          if [[ -f "$dep" ]]; then
            files+=("$dep")
          fi
          ;;
      esac
    done < <(modprobe --show-depends "$m" 2>/dev/null || true)
  done
  # Dedup and copy preserving relative path under /lib/modules/$kver
  local f rel dest
  declare -A seen=()
  for f in "${files[@]}"; do
    [[ -n "${seen[$f]:-}" ]] && continue
    seen[$f]=1
    rel="${f#/lib/modules/${kver}/}"
    if [[ "$rel" == "$f" ]]; then
      # unexpected path — copy by basename
      dest="$ROOT/lib/modules/${kver}/$(basename "$f")"
    else
      dest="$ROOT/lib/modules/${kver}/${rel}"
    fi
    mkdir -p "$(dirname "$dest")"
    cp -a "$f" "$dest"
    echo "module: $rel"
  done
  if command -v depmod >/dev/null 2>&1; then
    depmod -b "$ROOT" "$kver" 2>/dev/null || true
  fi
  # Marker for init
  printf '%s\n' "$kver" > "$ROOT/kver"
  echo "packed modules for kernel $kver (${#seen[@]} files)"
}
pack_modules

# Optional payload (Phase 5c text / Phase 6a binary image / Phase 7a OS raw|gz).
# Prefer SHOAL_PAYLOAD_FILE (path); else SHOAL_EMBEDDED_PAYLOAD (inline text).
#
# Large payloads MUST NOT go into the initrd: a ~680MB cloud image initrd OOMs /
# hangs nested 2GiB VMs (kernel must decompress the whole initramfs into RAM).
# Threshold: embed in initrd only if <= SHOAL_PAYLOAD_INITRD_MAX (default 4MiB);
# otherwise place on the ISO root and have /init mount the CDROM to stream it.
PAYLOAD_INITRD_MAX="${SHOAL_PAYLOAD_INITRD_MAX:-4194304}"
ISO_PAYLOAD_SRC=""   # host path to copy onto ISO (empty = none / already in initrd)
ISO_PAYLOAD_NAME=""  # name on ISO: payload or payload.gz
PAYLOAD_IS_GZIP=0

if [[ -n "${SHOAL_PAYLOAD_FILE:-}" && -r "${SHOAL_PAYLOAD_FILE}" ]]; then
  psz="$(wc -c <"${SHOAL_PAYLOAD_FILE}" | tr -d ' ')"
  if [[ "${SHOAL_PAYLOAD_FILE}" == *.gz ]] || gzip -t "${SHOAL_PAYLOAD_FILE}" 2>/dev/null; then
    PAYLOAD_IS_GZIP=1
  fi
  if [[ "$psz" -le "$PAYLOAD_INITRD_MAX" ]]; then
    cp "${SHOAL_PAYLOAD_FILE}" "$ROOT/payload"
    chmod 644 "$ROOT/payload"
    if [[ "$PAYLOAD_IS_GZIP" -eq 1 ]]; then
      printf '1\n' > "$ROOT/payload_gzip"
    fi
    echo "payload: embedded in initrd ($psz bytes gzip=${PAYLOAD_IS_GZIP})"
  else
    ISO_PAYLOAD_SRC="${SHOAL_PAYLOAD_FILE}"
    if [[ "$PAYLOAD_IS_GZIP" -eq 1 ]]; then
      ISO_PAYLOAD_NAME="payload.gz"
      printf '1\n' > "$ROOT/payload_on_iso_gzip"
    else
      ISO_PAYLOAD_NAME="payload"
      printf '0\n' > "$ROOT/payload_on_iso_gzip"
    fi
    printf '%s\n' "$ISO_PAYLOAD_NAME" > "$ROOT/payload_on_iso"
    echo "payload: on ISO as /${ISO_PAYLOAD_NAME} ($psz bytes gzip=${PAYLOAD_IS_GZIP}) — not in initrd"
  fi
elif [[ -n "${SHOAL_EMBEDDED_PAYLOAD:-}" ]]; then
  printf '%s' "${SHOAL_EMBEDDED_PAYLOAD}" > "$ROOT/payload"
  chmod 644 "$ROOT/payload"
fi

# Bake install defaults for init (read by the shell script below).
printf '%s\n' "$INSTALL_MODE" > "$ROOT/install_mode"
printf '%s\n' "$INSTALL_TARGET_DEFAULT" > "$ROOT/install_target_default"
printf '%s\n' "$INSTALL_REBOOT" > "$ROOT/install_reboot"
printf '%s\n' "$PREP_WIPE_LEVEL" > "$ROOT/prep_wipe_level"
# Optional NoCloud FAT image for config_drive — on ISO FS (not initrd; can be multi‑MiB).
ISO_SEED_SRC=""
if [[ -n "${SHOAL_SEED_IMG:-}" && -r "${SHOAL_SEED_IMG}" ]]; then
  ISO_SEED_SRC="${SHOAL_SEED_IMG}"
  printf 'seed.img\n' > "$ROOT/seed_on_iso"
  echo "seed.img: on ISO as /seed.img ($(wc -c <"${SHOAL_SEED_IMG}" | tr -d ' ') bytes) — not in initrd"
fi

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

# Load modules for SATA CD (ahci), ISO9660, and virtio disk (payload + /dev/vda).
load_mods() {
  KVER=""
  if [ -f /kver ]; then KVER="$(cat /kver 2>/dev/null | tr -d '\n')"; fi
  if [ -z "$KVER" ]; then KVER="$(uname -r 2>/dev/null || true)"; fi
  BASE="/lib/modules/$KVER"
  if [ -n "$KVER" ] && [ -d "$BASE" ]; then
    # Explicit paths (order matters). Suppress "File exists" if already loaded.
    for ko in \
      "$BASE/kernel/drivers/block/virtio_blk.ko" \
      "$BASE/kernel/drivers/ata/libahci.ko" \
      "$BASE/kernel/drivers/ata/ahci.ko" \
      "$BASE/kernel/fs/isofs/isofs.ko"
    do
      if [ -f "$ko" ]; then
        insmod "$ko" 2>/dev/null || true
      fi
    done
    # Catch any remaining .ko we packed
    for ko in "$BASE"/kernel/*/*.ko "$BASE"/kernel/*/*/*.ko "$BASE"/kernel/*/*/*/*.ko; do
      [ -f "$ko" ] || continue
      insmod "$ko" 2>/dev/null || true
    done
    modprobe isofs 2>/dev/null || true
    modprobe ahci 2>/dev/null || true
    modprobe virtio_blk 2>/dev/null || true
  fi
  mdev -s 2>/dev/null || true
  sleep 2
}
load_mods

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

# Resolve payload path: small embeds live at /payload; large OS images live on the CD.
PAYLOAD=""
GZIP=0
CD_MOUNTED=0
list_block_devs() {
  # Compact list for marker detail (max ~120 chars).
  out=""
  for d in /sys/block/*; do
    [ -e "$d" ] || continue
    n="${d##*/}"
    case "$n" in
      loop*|ram*|fd*) continue ;;
    esac
    out="${out}${n} "
  done
  for d in /dev/sr* /dev/cdrom /dev/vd* /dev/sd* /dev/hd*; do
    [ -e "$d" ] || [ -b "$d" ] || continue
    out="${out}${d##*/} "
  done
  echo "$out"
}

mount_cd() {
  mkdir -p /mnt/cd
  # Retry: modules/device nodes can appear slightly after boot.
  attempt=0
  while [ "$attempt" -lt 8 ]; do
    mdev -s 2>/dev/null || true
    # Re-try isofs each round
    if [ -f /kver ]; then
      k="$(cat /kver 2>/dev/null | tr -d '\n')"
      insmod "/lib/modules/$k/kernel/fs/isofs/isofs.ko" 2>/dev/null || true
      insmod "/lib/modules/$k/kernel/drivers/ata/libahci.ko" 2>/dev/null || true
      insmod "/lib/modules/$k/kernel/drivers/ata/ahci.ko" 2>/dev/null || true
      insmod "/lib/modules/$k/kernel/drivers/block/virtio_blk.ko" 2>/dev/null || true
    fi
    for dev in /dev/sr0 /dev/sr1 /dev/cdrom /dev/hdc /dev/hdb /dev/hda /dev/scd0 /dev/sdb /dev/sdc /dev/sda; do
      if [ -b "$dev" ] || [ -e "$dev" ]; then
        if mount -t iso9660 -o ro "$dev" /mnt/cd 2>/dev/null; then
          CD_MOUNTED=1
          return 0
        fi
        if mount -o ro "$dev" /mnt/cd 2>/dev/null; then
          CD_MOUNTED=1
          return 0
        fi
      fi
    done
    for dev in /dev/sr* /dev/vd* /dev/sd*; do
      if [ -b "$dev" ]; then
        if mount -t iso9660 -o ro "$dev" /mnt/cd 2>/dev/null; then
          CD_MOUNTED=1
          return 0
        fi
      fi
    done
    attempt=$((attempt + 1))
    sleep 1
  done
  return 1
}

if [ -f /payload ]; then
  PAYLOAD=/payload
  if [ -f /payload_gzip ]; then GZIP=1; fi
elif [ -f /payload_on_iso ]; then
  pname="$(cat /payload_on_iso 2>/dev/null | tr -d '\n')"
  [ -n "$pname" ] || pname=payload.gz
  emit BOOT 0 OK "mounting install media for payload=${pname}"
  if mount_cd; then
    if [ -f "/mnt/cd/${pname}" ]; then
      PAYLOAD="/mnt/cd/${pname}"
    elif [ -f /mnt/cd/payload.gz ]; then
      PAYLOAD=/mnt/cd/payload.gz
    elif [ -f /mnt/cd/payload ]; then
      PAYLOAD=/mnt/cd/payload
    fi
    if [ -f /payload_on_iso_gzip ]; then
      GZIP="$(cat /payload_on_iso_gzip 2>/dev/null | tr -d '\n' || echo 0)"
    fi
    case "$PAYLOAD" in
      *.gz) GZIP=1 ;;
    esac
  else
    devs="$(list_block_devs)"
    merr=""
    if [ -b /dev/sr0 ]; then
      merr="$(mount -t iso9660 -o ro /dev/sr0 /mnt/cd 2>&1 || true)"
    fi
    k="$(cat /kver 2>/dev/null | tr -d '\n')"
    imsg="$(insmod "/lib/modules/$k/kernel/fs/isofs/isofs.ko" 2>&1 || true)"
    # Keep detail short for SOL marker line length.
    emit ERROR 0 ERROR "cd mount fail devs=${devs} merr=${merr} insmod=${imsg}"
  fi
fi

boot_detail="marker live image started mode=${MODE}"
if [ -n "$PAYLOAD" ] && [ -f "$PAYLOAD" ]; then
  psz="$(wc -c <"$PAYLOAD" 2>/dev/null || echo 0)"
  boot_detail="marker live image started mode=${MODE} payload_bytes=${psz} gzip=${GZIP}"
fi
emit BOOT 0 OK "$boot_detail"
sleep 1
emit BOOT - HEARTBEAT ""

REBOOT="0"
if [ -f /install_reboot ]; then
  REBOOT="$(cat /install_reboot 2>/dev/null | tr -d '\n' || echo 0)"
fi

WIPE_LEVEL="discard"
if [ -f /prep_wipe_level ]; then
  WIPE_LEVEL="$(cat /prep_wipe_level 2>/dev/null | tr -d '\n' || echo discard)"
fi

if [ "$MODE" = "prep" ]; then
  emit PREP_BOOT 0 OK "prep live image started"
  sleep 1
  emit PREP_BOOT - HEARTBEAT ""
  if [ -z "$TARGET" ]; then
    for cand in /dev/vda /dev/sda /dev/nvme0n1; do
      if [ -b "$cand" ]; then TARGET="$cand"; break; fi
    done
  fi
  if [ -z "$TARGET" ] || [ ! -b "$TARGET" ]; then
    emit ERROR 0 ERROR "prep: no wipe target block device"
    sleep 2
    /bin/busybox poweroff -f 2>/dev/null || true
    while true; do sleep 3600; done
  fi
  emit PREP_WIPE 10 OK "wipe start target=${TARGET} level=${WIPE_LEVEL}"
  wiped=0
  if [ "$WIPE_LEVEL" = "discard" ]; then
    # busybox may not have blkdiscard; try if present
    if command -v blkdiscard >/dev/null 2>&1; then
      if blkdiscard -f "$TARGET" 2>/dev/null; then
        wiped=1
        emit PREP_WIPE 80 OK "blkdiscard ok target=${TARGET}"
      fi
    fi
  fi
  if [ "$wiped" = "0" ]; then
    # Destroy partition table + FS superblocks (first 64 MiB) — enough for reimage.
    emit PREP_WIPE 30 OK "zeroing start of ${TARGET}"
    (
      while true; do
        sleep 10
        emit PREP_WIPE - HEARTBEAT "zeroing ${TARGET}"
      done
    ) &
    HBPID=$!
    if dd if=/dev/zero of="$TARGET" bs=1M count=64 conv=fsync 2>/dev/null; then
      kill "$HBPID" 2>/dev/null || true
      emit PREP_WIPE 90 OK "zeroed 64MiB target=${TARGET}"
    else
      kill "$HBPID" 2>/dev/null || true
      emit ERROR 0 ERROR "prep wipe dd failed target=${TARGET}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
  fi
  sync 2>/dev/null || true
  emit PREP_WIPE 100 OK "wipe finished target=${TARGET}"

  # config_drive: write prebuilt FAT cidata image to end of disk.
  # seed.img lives on the install ISO (seed_on_iso), not in the initrd.
  SEED_PATH=""
  if [ -f /seed_on_iso ]; then
    emit PREP_SEED 5 OK "mounting media for seed.img"
    mkdir -p /mnt/cd
    SEED_PATH=""
    for tries in 1 2 3 4 5 6 7 8 9 10; do
      for cd in /dev/sr0 /dev/sr1 /dev/cdrom /dev/vdb /dev/sda; do
        [ -b "$cd" ] || continue
        if mount -t iso9660 -o ro "$cd" /mnt/cd 2>/dev/null; then
          if [ -f /mnt/cd/seed.img ]; then
            SEED_PATH=/mnt/cd/seed.img
            break 2
          fi
          umount /mnt/cd 2>/dev/null || true
        fi
      done
      sleep 1
    done
    if [ -z "$SEED_PATH" ]; then
      emit ERROR 0 ERROR "prep seed.img not found on install media"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
  elif [ -f /seed.img ]; then
    SEED_PATH=/seed.img
  fi
  if [ -n "$SEED_PATH" ]; then
    emit PREP_SEED 10 OK "writing config_drive seed to end of ${TARGET}"
    SEED_BYTES="$(wc -c <"$SEED_PATH" 2>/dev/null | tr -d ' ')"
    if [ -z "$SEED_BYTES" ] || [ "$SEED_BYTES" = "0" ]; then
      emit ERROR 0 ERROR "prep seed.img empty"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
    # /sys/block/<name>/size is 512-byte sectors for whole disk.
    DEVBASE="$(basename "$TARGET")"
    SECTORS="$(cat /sys/block/${DEVBASE}/size 2>/dev/null || echo 0)"
    SEED_SECTORS=$(( (SEED_BYTES + 511) / 512 ))
    if [ -z "$SECTORS" ] || [ "$SECTORS" -le "$SEED_SECTORS" ]; then
      emit ERROR 0 ERROR "prep seed: disk too small or size unknown target=${TARGET}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
    SEEK=$(( SECTORS - SEED_SECTORS ))
    if dd if="$SEED_PATH" of="$TARGET" bs=512 seek="$SEEK" conv=fsync 2>/dev/null; then
      sync 2>/dev/null || true
      emit PREP_SEED 100 OK "config_drive seed written label=cidata seek=${SEEK} sectors=${SEED_SECTORS}"
    else
      emit ERROR 0 ERROR "prep seed dd failed target=${TARGET}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
    umount /mnt/cd 2>/dev/null || true
  fi

  emit PREP_DONE 100 OK "prep complete ready for os install"
  # Stay powered on with heartbeats so the orchestrator can swap Virtual Media
  # and ForceRestart into the OS install ISO without losing the serial PTY
  # (poweroff left nested libvirt serial unusable for the next stage).
  while true; do
    sleep 15
    emit PREP_DONE - HEARTBEAT "awaiting os install media swap"
  done
fi

if [ "$MODE" = "write" ] && [ -n "$PAYLOAD" ] && [ -f "$PAYLOAD" ]; then
  emit DISK_PREP 5 OK "resolving install target"
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

  total="$(wc -c <"$PAYLOAD" 2>/dev/null || echo 0)"
  if [ -z "$total" ] || [ "$total" = "0" ]; then
    emit ERROR 0 ERROR "empty payload"
    sleep 2
    /bin/busybox poweroff -f 2>/dev/null || true
    while true; do sleep 3600; done
  fi

  emit IMAGE_WRITE 0 OK "write start target=${TARGET} bytes=${total} gzip=${GZIP} src=${PAYLOAD}"
  if [ "$GZIP" = "1" ]; then
    # Stream-decompress OS image to disk (Phase 7a cloud image path).
    # Progress heartbeats while gunzip|dd runs (no fine-grained percent).
    (
      while true; do
        sleep 20
        emit IMAGE_WRITE - HEARTBEAT "gunzip|dd in progress target=${TARGET}"
      done
    ) &
    HBPID=$!
    if gunzip -c "$PAYLOAD" 2>/dev/null | dd of="$TARGET" bs=4M conv=fsync 2>/dev/null; then
      kill "$HBPID" 2>/dev/null || true
      emit IMAGE_WRITE 100 OK "gzip write complete target=${TARGET}"
    else
      kill "$HBPID" 2>/dev/null || true
      emit ERROR 0 ERROR "gunzip|dd failed target=${TARGET}"
      sleep 2
      /bin/busybox poweroff -f 2>/dev/null || true
      while true; do sleep 3600; done
    fi
  else
    # Chunked copy for progress (1 MiB blocks).
    bs=1048576
    blocks=$(( total / bs ))
    rem=$(( total % bs ))
    written=0
    i=0
    while [ "$i" -lt "$blocks" ]; do
      dd if="$PAYLOAD" of="$TARGET" bs="$bs" count=1 skip="$i" seek="$i" conv=notrunc 2>/dev/null || {
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
      dd if="$PAYLOAD" of="$TARGET" bs=1 count="$rem" skip="$written" seek="$written" conv=notrunc 2>/dev/null || {
        emit ERROR 0 ERROR "dd failed on remainder"
        sleep 2
        /bin/busybox poweroff -f 2>/dev/null || true
        while true; do sleep 3600; done
      }
      written=$(( written + rem ))
    fi
    emit IMAGE_WRITE 100 OK "write complete target=${TARGET}"
  fi
  sync 2>/dev/null || true
  # Verify size on target when it is a regular file (not for gzip→block).
  if [ -f "$TARGET" ] && [ "$GZIP" != "1" ]; then
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
  emit POSTINSTALL 100 OK "payload installed"
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
if [ "${REBOOT:-0}" = "1" ]; then
  emit BOOT 100 OK "rebooting into installed system"
  sleep 1
  /bin/busybox reboot -f 2>/dev/null || /bin/busybox poweroff -f 2>/dev/null || true
else
  /bin/busybox poweroff -f 2>/dev/null || /bin/busybox reboot -f 2>/dev/null || true
fi
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
# Large OS payload on ISO root (streamed from CD by /init — not in initrd).
if [[ -n "${ISO_SEED_SRC:-}" && -r "${ISO_SEED_SRC}" ]]; then
  cp "$ISO_SEED_SRC" "$ISO/seed.img"
  echo "iso: /seed.img ($(wc -c <"$ISO/seed.img" | tr -d ' ') bytes)"
fi
if [[ -n "$ISO_PAYLOAD_SRC" && -n "$ISO_PAYLOAD_NAME" ]]; then
  cp "$ISO_PAYLOAD_SRC" "$ISO/$ISO_PAYLOAD_NAME"
  chmod 644 "$ISO/$ISO_PAYLOAD_NAME"
  echo "iso payload: $ISO/$ISO_PAYLOAD_NAME"
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
elif [[ "$INSTALL_MODE" = "prep" ]]; then
  VOLID="SHOAL_PREP"
fi

mkdir -p "$(dirname "$OUT")"
# -R/-J so payload.gz keeps its name when mounted (not ISO9660 PAYLOAD.GZ;1).
xorriso -as mkisofs \
  -quiet \
  -o "$OUT" \
  -V "$VOLID" \
  -R -J \
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
