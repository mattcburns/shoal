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

# modinfo/modprobe (kmod) live in /usr/sbin, which a non-login or restricted
# shell's PATH sometimes omits. pack_modules() below silently packs 0 files
# when they're "not found" (stderr suppressed to tolerate builtin-only
# kernels) -- harmless for simulate mode, which never mounts the CD from
# inside Linux, but fatal and silent for write/autoinstall/prep, which do.
# Confirmed live: an install fell through to the simulate demo sequence with
# no error because ahci/isofs never got packed, purely from this PATH gap.
export PATH="/usr/sbin:/sbin:/usr/local/sbin:$PATH"

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

BUSYBOX="${SHOAL_BUSYBOX:-}"
if [[ -z "$BUSYBOX" ]]; then
  BUSYBOX="$(command -v busybox || true)"
  if [[ -z "$BUSYBOX" ]]; then
    echo "error: busybox not found" >&2
    exit 1
  fi
  if [[ -x /bin/busybox ]]; then
    BUSYBOX=/bin/busybox
  fi
fi
# The initramfs packs only the busybox binary, no shared libs. A dynamically
# linked busybox cannot exec as /init (kernel panics "Failed to execute /init
# (error -2)" within ~3s, before /init even runs) — every boot then shows zero
# SHOAL| markers with no build-time signal that anything is wrong. Prefer a
# static build and fail loudly rather than silently packing a dead ISO.
if file "$BUSYBOX" 2>/dev/null | grep -q "dynamically linked"; then
  for alt in /usr/bin/busybox /bin/busybox-static /usr/bin/busybox-static /sbin/busybox-static; do
    if [[ -x "$alt" ]] && file "$alt" 2>/dev/null | grep -q "statically linked"; then
      BUSYBOX="$alt"
      break
    fi
  done
fi
if file "$BUSYBOX" 2>/dev/null | grep -q "dynamically linked"; then
  echo "error: $BUSYBOX is dynamically linked; it cannot run as /init in this" >&2
  echo "       minimal initramfs (no libc packed) and will panic on boot with" >&2
  echo "       zero markers. Install busybox-static (apt-get install busybox-static)" >&2
  echo "       or set SHOAL_BUSYBOX=/path/to/static/busybox." >&2
  exit 1
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

# UEFI boot catalog (in addition to the BIOS isolinux entry above). Many real
# BMCs (e.g. Dell iDRAC on current-gen PowerEdge) run BootSourceOverrideMode=UEFI
# and silently skip a CD with no UEFI boot image — it falls through to the next
# boot device instead of erroring. Soft-required: degrade to BIOS-only with a
# warning if grub-mkstandalone or the x86_64-efi module set is missing.
GRUB_MKSTANDALONE="$(command -v grub-mkstandalone || true)"
GRUB_EFI_MODDIR=""
for d in /usr/lib/grub/x86_64-efi /usr/lib/grub2/x86_64-efi; do
  if [[ -d "$d" ]]; then GRUB_EFI_MODDIR="$d"; break; fi
done
if ! command -v mformat >/dev/null 2>&1 || ! command -v mcopy >/dev/null 2>&1 || ! command -v mmd >/dev/null 2>&1; then
  GRUB_MKSTANDALONE=""
fi
BUILD_UEFI=1
if [[ -z "$GRUB_MKSTANDALONE" || -z "$GRUB_EFI_MODDIR" ]]; then
  BUILD_UEFI=0
  echo "warn: grub-mkstandalone/mtools/x86_64-efi modules not found — ISO will be BIOS-only (install grub-efi-amd64-bin + mtools for UEFI boot)" >&2
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

VOLID="SHOAL_MARKER"
if [[ "$INSTALL_MODE" = "write" ]]; then
  VOLID="SHOAL_INSTALL"
elif [[ "$INSTALL_MODE" = "prep" ]]; then
  VOLID="SHOAL_PREP"
fi

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
  # sr_mod/sd_mod are the SCSI upper-layer class drivers that actually create
  # /dev/sr*/sd* -- ahci+libata only expose the SCSI *transport*; the SCSI
  # core will happily log "scsi 0:0:0:0: CD-ROM ..." with no block device
  # ever appearing if the class driver for that device type isn't loaded
  # too. Neither is a dependency of ahci, so modprobe --show-depends ahci
  # alone never pulls them in. Confirmed live via a local QEMU repro: kernel
  # detected both the CD and disk over SCSI, module list all loaded
  # correctly, and /sys/block stayed empty the entire time regardless.
  # Dell iDRAC (and most real BMCs) present Virtual Media as a USB mass
  # storage device, not SATA/AHCI -- confirmed live: on the real R750 the
  # ahci/sr_mod/sd_mod chain above correctly found the two physical SATA
  # disks (devs=sda sdb) but zero CD device, because the CD was never on
  # that bus at all. QEMU's -cdrom (used for local validation) attaches
  # via AHCI/IDE by default, which is why that local repro didn't catch
  # this -- needs an explicit USB-attached scratch CD to match real BMC
  # behavior. usb_storage covers classic bulk-only; uas covers USB
  # Attached SCSI, which some BMCs use instead.
  for m in ahci libahci sr_mod sd_mod isofs \
    xhci_pci ehci_pci usb_storage uas \
    virtio_blk virtio_pci virtio_ring virtio; do
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
  # Dedup and copy preserving relative path under /lib/modules/$kver.
  # /init's insmod calls hardcode bare .ko paths, and busybox's insmod
  # applet cannot decompress modules itself -- most distro kernels (Debian
  # included) ship modules as .ko.xz. Decompress into place under the bare
  # .ko name so both the hardcoded paths and busybox insmod work.
  local f rel dest destdir
  declare -A seen=()
  for f in "${files[@]}"; do
    [[ -n "${seen[$f]:-}" ]] && continue
    seen[$f]=1
    rel="${f#/lib/modules/${kver}/}"
    rel="${rel%.xz}"
    rel="${rel%.zst}"
    rel="${rel%.gz}"
    if [[ "$rel" == "${f#/lib/modules/${kver}/}" && "$rel" == "$f" ]]; then
      # unexpected path — copy by basename
      dest="$ROOT/lib/modules/${kver}/$(basename "${f%.xz}")"
      dest="${dest%.zst}"
      dest="${dest%.gz}"
    else
      dest="$ROOT/lib/modules/${kver}/${rel}"
    fi
    destdir="$(dirname "$dest")"
    mkdir -p "$destdir"
    case "$f" in
      *.ko.xz) xz -dc "$f" > "$dest" ;;
      *.ko.zst) zstd -dc "$f" > "$dest" 2>/dev/null || { echo "error: zstd required to unpack $f" >&2; exit 1; } ;;
      *.ko.gz) gzip -dc "$f" > "$dest" ;;
      *) cp -a "$f" "$dest" ;;
    esac
    echo "module: $rel"
  done
  if command -v depmod >/dev/null 2>&1; then
    depmod -b "$ROOT" "$kver" 2>/dev/null || true
  fi
  # Marker for init
  printf '%s\n' "$kver" > "$ROOT/kver"
  echo "packed modules for kernel $kver (${#seen[@]} files)"
  PACKED_MODULE_COUNT="${#seen[@]}"
}
PACKED_MODULE_COUNT=0
pack_modules
# write/autoinstall/prep mount the CD from inside Linux to reach the payload
# (isofs + the CD's storage-controller driver); simulate never does. 0 packed
# modules there means the target kernel almost certainly cannot mount the CD
# at boot -- init then silently falls through to the simulate demo sequence
# with no error, up through and including a real disk-write job reporting
# success while never touching the disk. Fail the build loudly instead.
if [[ "$INSTALL_MODE" != "simulate" && "$PACKED_MODULE_COUNT" -eq 0 ]]; then
  echo "error: 0 kernel modules packed for mode=$INSTALL_MODE, which needs to" >&2
  echo "       mount the CD from inside Linux. modinfo/modprobe (kmod) must be" >&2
  echo "       on PATH and /lib/modules/\$(uname -r) must have ahci/isofs (or" >&2
  echo "       equivalent) as loadable modules for the kernel this ISO carries." >&2
  echo "       Check: modinfo -n isofs ; modinfo -n ahci" >&2
  exit 1
fi

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

# Init emits markers then powers off. Lab libvirt is ttyS0; iDRAC SOL is COM2 (ttyS1).
cat > "$ROOT/init" << 'INIT'
#!/bin/busybox sh
export PATH=/bin

/bin/busybox mount -t proc none /proc 2>/dev/null || true
/bin/busybox mount -t sysfs none /sys 2>/dev/null || true
/bin/busybox mount -t devtmpfs none /dev 2>/dev/null || true

# Fan-out markers to lab virtio (ttyS0) and iDRAC SOL COM2 (ttyS1).
serials=""
for dev in /dev/ttyS0 /dev/ttyS1; do
  if [ -w "$dev" ]; then
    serials="$serials $dev"
  fi
done
set -- $serials
if [ "$#" -ge 2 ]; then
  mkfifo /tmp/shoal-cons
  /bin/busybox tee $serials </tmp/shoal-cons >/dev/null &
  exec >/tmp/shoal-cons 2>&1
elif [ -n "$1" ]; then
  exec >"$1" 2>&1
else
  for dev in /dev/console /dev/tty0; do
    if [ -w "$dev" ]; then
      exec >"$dev" 2>&1
      break
    fi
  done
fi

# Load modules for SATA CD (ahci), ISO9660, and virtio disk (payload + /dev/vda).
#
# Use busybox's modprobe, not a hand-ordered insmod list: busybox insmod
# does not resolve dependencies itself, and the real chain here is deep and
# easy to get wrong by hand (scsi_common -> scsi_mod -> libata -> libahci
# -> ahci, PLUS sr_mod/sd_mod as independent SCSI class drivers that create
# /dev/sr*//dev/sd* -- neither is a dependency of ahci, so the SCSI core
# can log a device as detected while no block device ever appears if
# they're missing). depmod already generated a correct modules.dep for
# exactly this modprobe to use. Confirmed live via a local QEMU repro that
# an incomplete/misordered hand list silently produces zero block devices
# with no error anywhere -- a real disk-write job would then fall through
# to the simulate demo sequence and report success without ever touching
# the disk.
load_mods() {
  KVER=""
  if [ -f /kver ]; then KVER="$(cat /kver 2>/dev/null | tr -d '\n')"; fi
  if [ -z "$KVER" ]; then KVER="$(uname -r 2>/dev/null || true)"; fi
  BASE="/lib/modules/$KVER"
  if [ -n "$KVER" ] && [ -d "$BASE" ]; then
    for m in ahci sr_mod sd_mod isofs xhci_pci ehci_pci usb_storage uas virtio_blk; do
      modprobe "$m" 2>/dev/null || true
    done
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
    # Re-try each round via modprobe (see load_mods for why: resolves the
    # full scsi_common/scsi_mod/libata/libahci/ahci/sr_mod/sd_mod chain
    # correctly from modules.dep, which a hand-ordered insmod list does not).
    modprobe ahci 2>/dev/null || true
    modprobe sr_mod 2>/dev/null || true
    modprobe sd_mod 2>/dev/null || true
    modprobe isofs 2>/dev/null || true
    modprobe xhci_pci 2>/dev/null || true
    modprobe ehci_pci 2>/dev/null || true
    modprobe usb_storage 2>/dev/null || true
    modprobe uas 2>/dev/null || true
    modprobe virtio_blk 2>/dev/null || true
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
    imsg="$(modprobe ahci 2>&1 || true)"
    # Keep detail short for SOL marker line length.
    emit ERROR 0 ERROR "cd mount fail devs=${devs} merr=${merr} modprobe=${imsg}"
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
CMDLINE="initrd=/boot/initrd.img console=ttyS0,115200n8 console=ttyS1,115200n8 console=tty0 quiet shoal.mode=${INSTALL_MODE}"
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

# UEFI: grub standalone EFI binary that finds the ISO9660 root by searching for
# a known file (works regardless of whether firmware presents the El Torito FAT
# image or the outer ISO as the boot device) and chainloads the same kernel/initrd
# as isolinux, with the same cmdline.
if [[ "$BUILD_UEFI" -eq 1 ]]; then
  GRUB_CFG="$WORK/grub-standalone.cfg"
  cat > "$GRUB_CFG" << GRUBCFG
insmod part_gpt
insmod part_msdos
insmod iso9660
insmod search
insmod search_fs_file
insmod search_label
search --no-floppy --set=root --file /boot/vmlinuz
linux /boot/vmlinuz ${CMDLINE}
initrd /boot/initrd.img
boot
GRUBCFG
  EFI_STUB="$WORK/bootx64.efi"
  "$GRUB_MKSTANDALONE" \
    -O x86_64-efi \
    -o "$EFI_STUB" \
    --modules="part_gpt part_msdos iso9660 fat search search_fs_file search_label normal linux" \
    --fonts="" --themes="" --locales="" \
    "boot/grub/grub.cfg=$GRUB_CFG" 2>&1 | sed 's/^/grub-mkstandalone: /' >&2
  if [[ -s "$EFI_STUB" ]]; then
    EFI_IMG="$WORK/efiboot.img"
    stub_kb="$(( ($(wc -c < "$EFI_STUB") + 1023) / 1024 ))"
    img_kb=$(( stub_kb + 1024 ))
    if [[ "$img_kb" -lt 4096 ]]; then img_kb=4096; fi
    truncate -s "${img_kb}K" "$EFI_IMG"
    mformat -i "$EFI_IMG" ::
    mmd -i "$EFI_IMG" ::EFI
    mmd -i "$EFI_IMG" ::EFI/BOOT
    mcopy -i "$EFI_IMG" "$EFI_STUB" ::EFI/BOOT/BOOTX64.EFI
    mkdir -p "$ISO/boot"
    cp "$EFI_IMG" "$ISO/boot/efiboot.img"
    echo "uefi: boot/efiboot.img (${img_kb}KiB, stub $(wc -c < "$EFI_STUB") bytes)"
  else
    echo "warn: grub-mkstandalone produced no output — ISO will be BIOS-only" >&2
    BUILD_UEFI=0
  fi
fi

mkdir -p "$(dirname "$OUT")"
XORRISO_ARGS=(
  -as mkisofs
  -quiet
  -o "$OUT"
  -V "$VOLID"
  -R -J
  -b boot/isolinux/isolinux.bin
  -c boot/isolinux/boot.cat
  -no-emul-boot
  -boot-load-size 4
  -boot-info-table
)
if [[ "$BUILD_UEFI" -eq 1 ]]; then
  XORRISO_ARGS+=( -eltorito-alt-boot -e boot/efiboot.img -no-emul-boot )
fi
# -R/-J so payload.gz keeps its name when mounted (not ISO9660 PAYLOAD.GZ;1).
xorriso "${XORRISO_ARGS[@]}" "$ISO"

chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c < "$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) mode=$INSTALL_MODE"
echo "publish: copy into lab ISO dir and serve as http://<lab>:8080/$(basename "$OUT")"
echo "BMC-reachable (VM lab nodes): http://192.168.124.1:8080/$(basename "$OUT")"
