#!/usr/bin/env bash
# build-ubuntu-autoinstall-iso.sh — remaster an official Ubuntu Server live ISO
# with cloud-init autoinstall (nocloud) and SHOAL|… SOL markers (Phase 7a).
#
# Strategy: keep the original hybrid El Torito + MBR/GPT boot path via
#   xorriso -boot_image any replay
# and only inject /nocloud + patch GRUB/isolinux configs in-place on a copy.
# Full extract+mkisofs rebuild drops Ubuntu's UEFI/hybrid boot and often fails
# under sushy/libvirt.
#
# Requirements: xorriso, openssl, python3, find, sed, cp.
# Source ISO: official Ubuntu Server live-server amd64 (22.04+ recommended).
#
# Usage:
#   SHOAL_UBUNTU_ISO=/path/to/ubuntu-22.04.5-live-server-amd64.iso \
#     ./infra/scripts/build-ubuntu-autoinstall-iso.sh [/out/shoal-ubuntu-autoinstall.iso]
set -euo pipefail

OUT="${1:-${SHOAL_ISO_OUT:-./shoal-ubuntu-autoinstall.iso}}"
if [[ -n "${SHOAL_ISO_NAME:-}" ]]; then
  OUT_DIR="$(dirname "$OUT")"
  OUT="${OUT_DIR%/}/${SHOAL_ISO_NAME}"
fi

SRC="${SHOAL_UBUNTU_ISO:-}"
if [[ -z "$SRC" || ! -r "$SRC" ]]; then
  cat >&2 <<'EOF'
error: set SHOAL_UBUNTU_ISO to a readable official Ubuntu Server live-server ISO.

Example:
  wget -O /var/tmp/ubuntu-22.04.5-live-server-amd64.iso \
    https://releases.ubuntu.com/22.04/ubuntu-22.04.5-live-server-amd64.iso
  export SHOAL_UBUNTU_ISO=/var/tmp/ubuntu-22.04.5-live-server-amd64.iso
EOF
  exit 1
fi

for need in xorriso openssl python3 find sed cp; do
  if ! command -v "$need" >/dev/null 2>&1; then
    echo "error: $need required" >&2
    exit 1
  fi
done

HOSTNAME="${SHOAL_AUTOINSTALL_HOSTNAME:-shoal-node}"
USERNAME="${SHOAL_AUTOINSTALL_USERNAME:-shoal}"
PASSWORD="${SHOAL_AUTOINSTALL_PASSWORD:-shoal-lab}"
PASSWORD_HASH="$(openssl passwd -6 "$PASSWORD")"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="${SHOAL_AUTOINSTALL_TEMPLATE:-$SCRIPT_DIR/autoinstall/ubuntu-user-data.yaml.tmpl}"
if [[ ! -r "$TEMPLATE" ]]; then
  echo "error: user-data template not found: $TEMPLATE" >&2
  exit 1
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/shoal-ubuntu-ai.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

echo "source:   $SRC"
echo "hostname: $HOSTNAME"
echo "user:     $USERNAME"
echo "output:   $OUT"

NOCLOUD="$WORK/nocloud"
mkdir -p "$NOCLOUD"

# Render user-data without sed '$' backref issues (password hashes contain $).
python3 - "$TEMPLATE" "$HOSTNAME" "$USERNAME" "$PASSWORD_HASH" "$NOCLOUD/user-data" <<'PY'
import sys
tmpl, hostname, username, pw_hash, out = sys.argv[1:6]
text = open(tmpl, encoding="utf-8").read()
text = text.replace("{{HOSTNAME}}", hostname)
text = text.replace("{{USERNAME}}", username)
text = text.replace("{{PASSWORD_HASH}}", pw_hash)
open(out, "w", encoding="utf-8").write(text)
PY
printf 'instance-id: shoal-autoinstall\nlocal-hostname: %s\n' "$HOSTNAME" > "$NOCLOUD/meta-data"
chmod -R a+rX "$NOCLOUD"

# Extract boot configs to patch, then feed them back with -update.
CFG_DIR="$WORK/cfgs"
mkdir -p "$CFG_DIR"
# List interesting paths in the source ISO.
mapfile -t CFG_PATHS < <(
  xorriso -indev "$SRC" -find / -name grub.cfg -or -name txt.cfg -or -name isolinux.cfg -or -name loopback.cfg 2>/dev/null \
    | sed -n 's/^.*'\''\(\/.*\)'\''$/\1/p' || true
)
# Fallback known paths if find parsing fails.
if [[ ${#CFG_PATHS[@]} -eq 0 ]]; then
  CFG_PATHS=(
    /boot/grub/grub.cfg
    /boot/grub/loopback.cfg
    /isolinux/txt.cfg
    /isolinux/isolinux.cfg
  )
fi

patch_autoinstall_cmdline() {
  local f="$1"
  [[ -f "$f" ]] || return 0
  # Ensure autoinstall nocloud + serial on casper kernel lines.
  if ! grep -q 'autoinstall' "$f" 2>/dev/null; then
    sed -i -E \
      's|(linux(efi)?[[:space:]]+/casper/vmlinuz[^[:space:]]*)|\1 autoinstall ds=nocloud\;s=/cdrom/nocloud/ console=ttyS0,115200n8|g' \
      "$f" || true
  fi
  if ! grep -q 'console=ttyS0' "$f" 2>/dev/null; then
    sed -i -E \
      's|(linux(efi)?[[:space:]]+/casper/vmlinuz[^[:space:]]*)|\1 console=ttyS0,115200n8|g' \
      "$f" || true
  fi
  # Unattended: no 30s GRUB pause; default first entry; talk on serial for SOL.
  if grep -q 'set timeout=' "$f" 2>/dev/null; then
    sed -i -E 's/^set timeout=.*/set timeout=0/' "$f" || true
  else
    # Prepend timeout if file is a main grub.cfg
    if grep -q 'menuentry' "$f" 2>/dev/null; then
      sed -i '1iset timeout=0\nset default=0' "$f" || true
    fi
  fi
  if grep -q 'menuentry' "$f" 2>/dev/null && ! grep -q 'serial --unit=0' "$f" 2>/dev/null; then
    sed -i '1iserial --unit=0 --speed=115200\nterminal_input serial console\nterminal_output serial console' "$f" || true
  fi
}

UPDATE_ARGS=()
for p in "${CFG_PATHS[@]}"; do
  # Normalize path
  p="/${p#/}"
  local_name="$CFG_DIR/$(echo "$p" | tr '/' '_')"
  if xorriso -osirrox on -indev "$SRC" -extract "$p" "$local_name" >/dev/null 2>&1; then
    patch_autoinstall_cmdline "$local_name"
    UPDATE_ARGS+=(-update "$local_name" "$p")
    echo "patched $p"
  fi
done

# If no casper lines were patched, append a GRUB menuentry file and source it
# only when we successfully extracted grub.cfg.
if [[ -f "$CFG_DIR/_boot_grub_grub.cfg" ]] && ! grep -q 'autoinstall' "$CFG_DIR/_boot_grub_grub.cfg" 2>/dev/null; then
  cat >> "$CFG_DIR/_boot_grub_grub.cfg" <<'GRUB'

# Shoal Phase 7a autoinstall entry (fallback)
menuentry "Shoal Ubuntu Autoinstall" {
    set gfxpayload=keep
    linux   /casper/vmlinuz autoinstall ds=nocloud\;s=/cdrom/nocloud/ console=ttyS0,115200n8 ---
    initrd  /casper/initrd
}
GRUB
  echo "appended fallback GRUB menuentry"
fi

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"

echo "repacking ISO (preserve original boot)..."
# -boot_image any replay keeps El Torito + hybrid MBR/GPT from the source.
# -map injects nocloud; -update replaces patched boot configs.
# Note: avoid -chmod modes that trigger xorriso SORRY (non-zero exit confuses callers).
set +e
xorriso \
  -indev "$SRC" \
  -outdev "$OUT" \
  -boot_image any replay \
  -map "$NOCLOUD" /nocloud \
  "${UPDATE_ARGS[@]+"${UPDATE_ARGS[@]}"}" \
  -commit
xr=$?
set -e
if [[ ! -s "$OUT" ]]; then
  echo "error: xorriso failed to produce $OUT (exit $xr)" >&2
  exit 1
fi
# xorriso may return 32 (SORRY) for non-fatal warnings; accept if ISO looks hybrid/bootable.
if [[ "$xr" -ne 0 && "$xr" -ne 32 ]]; then
  echo "error: xorriso exit $xr" >&2
  exit "$xr"
fi

chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c <"$OUT" | tr -d ' ')"

echo "=== boot report (should show BIOS+UEFI like upstream) ==="
xorriso -indev "$OUT" -report_el_torito plain 2>&1 | head -25 || true

echo "built $OUT ($SIZE bytes) mode=autoinstall"
echo "lab login (default): user=${USERNAME} password=<lab default shoal-lab unless overridden>"
echo "publish: copy to lab ISO dir; BMC URL e.g. http://192.168.124.1:8080/$(basename "$OUT")"
echo "deploy with raised SOL stall, e.g. -stall-timeout 45m -wait-timeout 90m"
