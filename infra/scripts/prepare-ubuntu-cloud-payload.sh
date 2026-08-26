#!/usr/bin/env bash
# prepare-ubuntu-cloud-payload.sh — customize an Ubuntu cloud image raw disk
# for Phase 7a nested-lab install (password login + hostname + cloud-init seed).
#
# Requires: qemu-img, losetup, mount, sudo (root for loop mounts).
#
# Usage:
#   SHOAL_UBUNTU_CLOUD_IMG=/path/to/ubuntu-22.04-server-cloudimg-amd64.img \
#     ./infra/scripts/prepare-ubuntu-cloud-payload.sh [/out/ubuntu-payload.raw]
set -euo pipefail

OUT="${1:-${SHOAL_CLOUD_PAYLOAD_OUT:-./ubuntu-cloud-payload.raw}}"
IMG="${SHOAL_UBUNTU_CLOUD_IMG:-}"
HOSTNAME="${SHOAL_AUTOINSTALL_HOSTNAME:-shoal-node}"
USERNAME="${SHOAL_AUTOINSTALL_USERNAME:-ubuntu}"
PASSWORD="${SHOAL_AUTOINSTALL_PASSWORD:-shoal-lab}"

if [[ -z "$IMG" || ! -r "$IMG" ]]; then
  cat >&2 <<'EOF'
error: set SHOAL_UBUNTU_CLOUD_IMG to an Ubuntu cloud image (.img).

Example:
  wget -O /var/tmp/ubuntu-22.04-server-cloudimg-amd64.img \
    https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img
  export SHOAL_UBUNTU_CLOUD_IMG=/var/tmp/ubuntu-22.04-server-cloudimg-amd64.img
EOF
  exit 1
fi

if [[ "$(id -u)" -ne 0 ]]; then
  echo "re-exec with sudo for loop mounts..."
  # -E (preserve caller env) is unnecessary here -- every var the script
  # needs is already reconstructed explicitly below via `env VAR=val`, and
  # -E requires SETENV in sudoers, which some restricted sudo policies
  # (NOPASSWD scoped to a specific wrapper) don't grant.
  exec sudo env \
    "SHOAL_UBUNTU_CLOUD_IMG=$IMG" \
    "SHOAL_AUTOINSTALL_HOSTNAME=$HOSTNAME" \
    "SHOAL_AUTOINSTALL_USERNAME=$USERNAME" \
    "SHOAL_AUTOINSTALL_PASSWORD=$PASSWORD" \
    "SHOAL_CLOUD_PAYLOAD_OUT=$OUT" \
    "$0" "$OUT"
fi

for need in qemu-img losetup mount umount blkid; do
  command -v "$need" >/dev/null || { echo "error: $need required" >&2; exit 1; }
done

WORK="$(mktemp -d /tmp/shoal-cloud-prep.XXXXXX)"
cleanup() {
  set +e
  # Unmount nested boot mount before its parent.
  umount "$WORK/mnt/boot" 2>/dev/null
  umount "$WORK/mnt" 2>/dev/null
  losetup -d "$LOOP" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "source cloud img: $IMG"
echo "hostname: $HOSTNAME user: $USERNAME"
echo "output raw: $OUT"

mkdir -p "$(dirname "$OUT")"
qemu-img convert -O raw "$IMG" "$OUT"
# Grow a bit for packages/logs
qemu-img resize "$OUT" +1G 2>/dev/null || true

LOOP="$(losetup -fP --show "$OUT")"
echo "loop: $LOOP"
sleep 1
# Prefer largest partition as root
ROOT_PART=""
for p in "${LOOP}p2" "${LOOP}p1" "${LOOP}p3"; do
  if [[ -b "$p" ]]; then
    ROOT_PART="$p"
    # Prefer ext* labels
    FSTYPE="$(blkid -o value -s TYPE "$p" 2>/dev/null || true)"
    if [[ "$FSTYPE" == ext4 || "$FSTYPE" == ext3 ]]; then
      break
    fi
  fi
done
if [[ -z "$ROOT_PART" ]]; then
  echo "error: no partition found on $OUT" >&2
  exit 1
fi
echo "root part: $ROOT_PART ($(blkid -o value -s TYPE "$ROOT_PART" 2>/dev/null || echo unknown))"

mkdir -p "$WORK/mnt"
mount "$ROOT_PART" "$WORK/mnt"

# Hostname
echo "$HOSTNAME" > "$WORK/mnt/etc/hostname"
if [[ -f "$WORK/mnt/etc/hosts" ]]; then
  if grep -q '^10.0.2.3' "$WORK/mnt/etc/hosts" 2>/dev/null; then
    sed -i "s/^10.0.2.3.*/10.0.2.3\t${HOSTNAME}/" "$WORK/mnt/etc/hosts" || true
  else
    echo -e "10.0.2.3\t${HOSTNAME}" >> "$WORK/mnt/etc/hosts"
  fi
fi

# Ensure user exists (cloud images usually have ubuntu)
if ! grep -q "^${USERNAME}:" "$WORK/mnt/etc/passwd" 2>/dev/null; then
  chroot "$WORK/mnt" useradd -m -s /bin/bash "$USERNAME" 2>/dev/null || true
fi
echo "${USERNAME}:${PASSWORD}" | chroot "$WORK/mnt" chpasswd 2>/dev/null \
  || echo "${USERNAME}:${PASSWORD}" | chpasswd -R "$WORK/mnt" 2>/dev/null \
  || true

# SSH password auth for lab
if [[ -f "$WORK/mnt/etc/ssh/sshd_config" ]]; then
  sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' "$WORK/mnt/etc/ssh/sshd_config" || true
  sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' "$WORK/mnt/etc/ssh/sshd_config" || true
fi
# cloud-init: provide nocloud seed so first boot is not stuck
mkdir -p "$WORK/mnt/var/lib/cloud/seed/nocloud"
cat > "$WORK/mnt/var/lib/cloud/seed/nocloud/meta-data" <<EOF
instance-id: shoal-${HOSTNAME}
local-hostname: ${HOSTNAME}
EOF
cat > "$WORK/mnt/var/lib/cloud/seed/nocloud/user-data" <<EOF
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
runcmd:
  - [ systemctl, enable, ssh ]
  - [ systemctl, restart, ssh ]
EOF
# Prefer nocloud seed over network datasources for lab offline boot
mkdir -p "$WORK/mnt/etc/cloud/cloud.cfg.d"
cat > "$WORK/mnt/etc/cloud/cloud.cfg.d/99-shoal-nocloud.cfg" <<'EOF'
datasource_list: [ NoCloud, None ]
EOF

# Serial console for SOL after install. Lab libvirt bridges ttyS0; real BMC
# SOL (iDRAC and friends) bridges COM2/ttyS1 -- add both so the installed OS
# is visible over SOL on either path (see build-marker-iso.sh's own dual-UART
# fanout for the same reasoning).
#
# Ubuntu 24.04 cloud images ship /boot as its OWN partition (LABEL=BOOT, see
# fstab), separate from rootfs, with GRUB already installed and grub.cfg
# already generated at image-build time -- editing only /etc/default/grub
# and running `chroot update-grub` (as this script used to) does nothing
# useful: update-grub can't find/write /boot without it bind-mounted into
# the chroot (needs /proc, /sys, /dev too for grub-probe), so it silently
# no-ops, and the pre-baked grub.cfg -- the one actually used at boot --
# never gets our console= addition. Confirmed by mounting a written disk
# image and finding /boot/grub.cfg unmodified. Fix: mount the real /boot
# partition and edit its grub.cfg directly instead of trying to regenerate
# it. (The ESP's grub.cfg is just `configfile` chainloading to this one --
# no separate edit needed there.)
if [[ -f "$WORK/mnt/etc/default/grub" ]]; then
  if ! grep -q 'console=ttyS0' "$WORK/mnt/etc/default/grub"; then
    sed -i 's/GRUB_CMDLINE_LINUX="/GRUB_CMDLINE_LINUX="console=ttyS0,115200n8 console=ttyS1,115200n8 /' "$WORK/mnt/etc/default/grub" || true
  fi
  if [[ -f "$WORK/mnt/etc/default/grub.d/50-cloudimg-settings.cfg" ]]; then
    sed -i 's#console=ttyS0,115200#console=ttyS0,115200n8 console=ttyS1,115200n8#' \
      "$WORK/mnt/etc/default/grub.d/50-cloudimg-settings.cfg" 2>/dev/null || true
  fi
fi
BOOT_PART="$(blkid -L BOOT 2>/dev/null || true)"
if [[ -n "$BOOT_PART" ]]; then
  mkdir -p "$WORK/mnt/boot"
  if mount "$BOOT_PART" "$WORK/mnt/boot" 2>/dev/null; then
    GRUB_CFG="$WORK/mnt/boot/grub/grub.cfg"
    if [[ -f "$GRUB_CFG" ]] && ! grep -q 'console=ttyS1' "$GRUB_CFG"; then
      sed -i 's/console=ttyS0\b/console=ttyS0 console=ttyS1,115200n8/g' "$GRUB_CFG" || true
      echo "grub.cfg: added console=ttyS1,115200n8 ($(grep -c 'console=ttyS1' "$GRUB_CFG" || true) entries)"
    fi
    umount "$WORK/mnt/boot"
  else
    echo "warn: found BOOT partition ($BOOT_PART) but could not mount it -- grub.cfg not edited, installed OS may not be visible over real-BMC SOL (ttyS1)" >&2
  fi
else
  echo "warn: no BOOT-labeled partition found -- grub.cfg not edited (older/different image layout?)" >&2
fi

sync
umount "$WORK/mnt"
losetup -d "$LOOP"
LOOP=""

# Optional gzip for smaller ISO payload
if [[ "${SHOAL_CLOUD_PAYLOAD_GZIP:-1}" == "1" ]]; then
  echo "compressing payload..."
  gzip -1 -c "$OUT" > "${OUT}.gz"
  mv "${OUT}.gz" "${OUT}.gz.tmp"
  # Keep raw name .raw.gz
  GOUT="${OUT%.raw}.raw.gz"
  if [[ "$OUT" == *.raw ]]; then
    GOUT="${OUT}.gz"
  else
    GOUT="${OUT}.gz"
  fi
  mv "${OUT}.gz.tmp" "$GOUT"
  ls -lh "$OUT" "$GOUT"
  echo "prepared $GOUT (use as SHOAL_PAYLOAD_FILE for install-mode write/autoinstall)"
  echo "raw also at $OUT"
else
  ls -lh "$OUT"
  echo "prepared $OUT"
fi
