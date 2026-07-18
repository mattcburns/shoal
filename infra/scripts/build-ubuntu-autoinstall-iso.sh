#!/usr/bin/env bash
# build-ubuntu-autoinstall-iso.sh — remaster an official Ubuntu Server live ISO
# with cloud-init autoinstall (nocloud) and SHOAL|… SOL markers (Phase 7a).
#
# Requirements: xorriso, openssl, find, sed.
# Source ISO: official Ubuntu Server live-server amd64 (22.04+ recommended).
#
# Usage:
#   SHOAL_UBUNTU_ISO=/path/to/ubuntu-22.04.5-live-server-amd64.iso \
#     ./infra/scripts/build-ubuntu-autoinstall-iso.sh [/out/shoal-ubuntu-autoinstall.iso]
#
# Env:
#   SHOAL_UBUNTU_ISO              Required path to base Ubuntu Server ISO
#   SHOAL_ISO_OUT / $1            Output path (default ./shoal-ubuntu-autoinstall.iso)
#   SHOAL_ISO_NAME                Basename override
#   SHOAL_AUTOINSTALL_HOSTNAME    default shoal-node
#   SHOAL_AUTOINSTALL_USERNAME    default shoal
#   SHOAL_AUTOINSTALL_PASSWORD    lab-only default (hashed at build; not for production)
#   SHOAL_AUTOINSTALL_TEMPLATE    optional path to user-data template
#
# Lab note: nested guests need a disk (lab nodes: 20G qcow2) and ≥2 GiB RAM.
# Raise SOL stall timeouts for full installs (e.g. 45m).
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

Example (download once to the lab host):
  wget -O /var/tmp/ubuntu-22.04.5-live-server-amd64.iso \
    https://releases.ubuntu.com/22.04/ubuntu-22.04.5-live-server-amd64.iso
  export SHOAL_UBUNTU_ISO=/var/tmp/ubuntu-22.04.5-live-server-amd64.iso
EOF
  exit 1
fi

for need in xorriso openssl find sed; do
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

ISO_TREE="$WORK/iso"
mkdir -p "$ISO_TREE"

echo "extracting base ISO..."
xorriso -osirrox on -indev "$SRC" -extract / "$ISO_TREE"
chmod -R u+w "$ISO_TREE" 2>/dev/null || true

NOCLOUD="$ISO_TREE/nocloud"
mkdir -p "$NOCLOUD"

# Escape sed replacement carefully for hash ($ and /).
esc_hash="$(printf '%s' "$PASSWORD_HASH" | sed -e 's/[&|\\]/\\&/g')"
sed \
  -e "s/{{HOSTNAME}}/${HOSTNAME}/g" \
  -e "s/{{USERNAME}}/${USERNAME}/g" \
  -e "s|{{PASSWORD_HASH}}|${esc_hash}|g" \
  "$TEMPLATE" > "$NOCLOUD/user-data"
printf 'instance-id: shoal-autoinstall\nlocal-hostname: %s\n' "$HOSTNAME" > "$NOCLOUD/meta-data"
chmod -R a+rX "$NOCLOUD"

patch_autoinstall_cmdline() {
  local f="$1"
  [[ -f "$f" ]] || return 0
  if grep -q 'autoinstall' "$f" 2>/dev/null; then
    # Still ensure serial console.
    if ! grep -q 'console=ttyS0' "$f" 2>/dev/null; then
      sed -i -E 's|(linux(efi)?[[:space:]]+/casper/vmlinuz[^[:space:]]*)|\1 console=ttyS0,115200n8|g' "$f" || true
    fi
    return 0
  fi
  # Append autoinstall nocloud datasource + serial console to casper kernel lines.
  sed -i -E \
    's|(linux(efi)?[[:space:]]+/casper/vmlinuz[^[:space:]]*)|\1 autoinstall ds=nocloud\;s=/cdrom/nocloud/ console=ttyS0,115200n8|g' \
    "$f" || true
}

while IFS= read -r -d '' cfg; do
  patch_autoinstall_cmdline "$cfg"
done < <(find "$ISO_TREE" \( -name 'grub.cfg' -o -name 'txt.cfg' -o -name 'isolinux.cfg' -o -name 'loopback.cfg' \) -print0 2>/dev/null)

if ! grep -Rqs 'autoinstall' "$ISO_TREE" --include='*.cfg' 2>/dev/null; then
  echo "warning: boot configs lacked casper lines; appending GRUB menuentry" >&2
  for g in "$ISO_TREE/boot/grub/grub.cfg" "$ISO_TREE/EFI/boot/grub.cfg"; do
    if [[ -f "$g" ]]; then
      cat >> "$g" <<'GRUB'

# Shoal Phase 7a autoinstall entry
menuentry "Shoal Ubuntu Autoinstall" {
    set gfxpayload=keep
    linux   /casper/vmlinuz autoinstall ds=nocloud\;s=/cdrom/nocloud/ console=ttyS0,115200n8 ---
    initrd  /casper/initrd
}
GRUB
      break
    fi
  done
fi

mkdir -p "$(dirname "$OUT")"

BOOT_IMG=""
for cand in \
  "$ISO_TREE/boot/grub/i386-pc/eltorito.img" \
  "$ISO_TREE/isolinux/isolinux.bin" \
  "$ISO_TREE/boot/isolinux/isolinux.bin"
do
  if [[ -f "$cand" ]]; then BOOT_IMG="${cand#"$ISO_TREE"/}"; break; fi
done

EFI_IMG=""
found="$(find "$ISO_TREE" -name 'efi.img' 2>/dev/null | head -1 || true)"
if [[ -n "$found" ]]; then
  EFI_IMG="${found#"$ISO_TREE"/}"
fi

echo "repacking ISO..."
XORRISO_ARGS=(
  -as mkisofs
  -r -V "SHOAL_UBUNTU_AI"
  -o "$OUT"
  -J -joliet-long
  -l
)
if [[ -n "$BOOT_IMG" && -f "$ISO_TREE/$BOOT_IMG" ]]; then
  if [[ "$BOOT_IMG" == *isolinux* ]]; then
    XORRISO_ARGS+=(-b "$BOOT_IMG" -c boot.catalog -no-emul-boot -boot-load-size 4 -boot-info-table)
  else
    XORRISO_ARGS+=(-b "$BOOT_IMG" -no-emul-boot -boot-load-size 4 -boot-info-table)
  fi
fi
if [[ -n "$EFI_IMG" && -f "$ISO_TREE/$EFI_IMG" ]]; then
  XORRISO_ARGS+=(-eltorito-alt-boot -e "$EFI_IMG" -no-emul-boot -isohybrid-gpt-basdat)
fi
XORRISO_ARGS+=("$ISO_TREE")

xorriso "${XORRISO_ARGS[@]}"

chmod 644 "$OUT" 2>/dev/null || true
SIZE="$(wc -c <"$OUT" | tr -d ' ')"
echo "built $OUT ($SIZE bytes) mode=autoinstall"
echo "lab login (default): user=${USERNAME} password=<lab default shoal-lab unless overridden>"
echo "publish: copy to lab ISO dir; BMC URL e.g. http://192.168.124.1:8080/$(basename "$OUT")"
echo "deploy with raised SOL stall, e.g. -stall-timeout 45m -wait-timeout 90m"
