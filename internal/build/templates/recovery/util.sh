#!/sbin/sh
# Shared helpers for the 306gapps recovery installer.

ui_print() {
  echo "ui_print $1" >> /proc/self/fd/"$OUTFD"
  echo "ui_print" >> /proc/self/fd/"$OUTFD"
}

abort() {
  ui_print " "
  ui_print "! $1"
  cleanup
  exit 1
}

log() { [ "$VERBOSE" = "1" ] && ui_print "  . $1"; return 0; }

# human <bytes> -> "123.4 MB"
# Compare and add sizes that may exceed 2 GiB.
#
# Recovery's shell does 32-bit signed arithmetic, in both [ and $(( )). A
# partition with 2541121536 bytes free wraps to -1753845760, so
# [ "$free" -lt "$need" ] answers "2.4 GB is less than 1.7 GB" and the
# installer refuses to write to a partition with room to spare. awk works in
# doubles and is exact well past any partition size.
lt() { awk -v a="$1" -v b="$2" 'BEGIN{ exit !(a+0 < b+0) }'; }
sub_bytes() { awk -v a="$1" -v b="$2" 'BEGIN{ printf "%.0f", a-b }'; }
add_bytes() { awk -v a="$1" -v b="$2" 'BEGIN{ printf "%.0f", a+b }'; }

human() {
  awk -v b="$1" 'BEGIN{
    split("B KB MB GB",u," "); i=1
    while (b>=1024 && i<4) { b/=1024; i++ }
    printf (i==1 ? "%d %s" : "%.1f %s"), b, u[i]
  }'
}

# Resolve the block device currently backing a mountpoint.
block_for() {
  grep " $1 " /proc/mounts 2>/dev/null | head -n1 | awk '{print $1}'
}

# The A/B slot suffix, e.g. "_a". Empty on non-A/B devices.
#
# Recovery does not always export it, so fall back to the kernel command line
# and to the older ro.boot.slot spelling.
find_slot() {
  slot=$(getprop ro.boot.slot_suffix 2>/dev/null)
  [ -z "$slot" ] && slot=$(grep -o 'androidboot.slot_suffix=[^ ]*' /proc/cmdline 2>/dev/null | cut -d= -f2)
  if [ -z "$slot" ]; then
    slot=$(getprop ro.boot.slot 2>/dev/null)
    [ -z "$slot" ] && slot=$(grep -o 'androidboot.slot=[^ ]*' /proc/cmdline 2>/dev/null | cut -d= -f2)
    [ -n "$slot" ] && slot=_$slot
  fi
  # Neither the property nor the command line is guaranteed in recovery. What
  # recovery has already mounted from super names the active slot, and picking
  # the wrong one would write to the slot the phone is not booting.
  if [ -z "$slot" ]; then
    case $(grep -o '/dev/block/mapper/[a-z_]*_[ab] ' /proc/mounts 2>/dev/null | head -n1) in
      *_a\ ) slot=_a ;;
      *_b\ ) slot=_b ;;
    esac
  fi
  echo "$slot"
}

# Every directory that may hold a partition by name, most specific first.
#
# This used to return only the first one that existed. A device with dynamic
# partitions has /dev/block/mapper, but recovery maps only what its fstab
# asks for, so a partition missing from there was never looked for anywhere
# else and the install stopped at "cannot mount /product".
block_paths() {
  [ -d /dev/block/mapper ] && echo /dev/block/mapper
  [ -d /dev/block/bootdevice/by-name ] && echo /dev/block/bootdevice/by-name
  find /dev/block/platform -type d -name by-name 2>/dev/null
  return 0
}

# Locate the block device for a partition by name, honouring the active slot.
# Needed when recovery has not already mounted the partition for us.
find_block() {
  name=$1
  slot=$(find_slot)
  names="$name$slot $name"
  # System-as-root devices may present /system under another name.
  [ "$name" = "system" ] && names="$names system_root$slot system_root"

  for base in $(block_paths); do
    for candidate in $names; do
      [ -b "$base/$candidate" ] && echo "$base/$candidate" && return 0
    done
  done
  return 1
}

# Clear the read-only flag on every partition we may write, across both slots.
# Doing this defensively is cheaper than diagnosing a silent EROFS later.
unlock_blocks() {
  [ -d /dev/block/mapper ] || return 0
  for block in system product system_ext; do
    for slot in "" _a _b; do
      [ -b "/dev/block/mapper/$block$slot" ] &&
        blockdev --setrw "/dev/block/mapper/$block$slot" 2>/dev/null
    done
  done
  return 0
}

# Confirm a mountpoint really is writable by writing to it. A successful
# remount does not guarantee this on dynamic partitions.
is_writable() {
  probe="$1/.306gapps.rw"
  touch "$probe" 2>/dev/null || return 1
  rm -f "$probe"
  return 0
}

# Make a mounted partition writable, handling dynamic (super) partitions where
# the underlying dm device is read-only until explicitly flipped.
make_rw() {
  mnt=$1
  dev=$(block_for "$mnt")

  case "$dev" in
    /dev/block/dm-*|/dev/block/mapper/*)
      blockdev --setrw "$dev" 2>/dev/null
      # /dev/block/mapper/product_b is a symlink to /dev/block/dm-N, and the
      # sysfs knob is named for the dm device. basename on the link looked for
      # /sys/block/product_b/force_ro, which does not exist, so the flag was
      # never cleared. Dynamic partitions come up read-only, which made the
      # remount below a no-op and every write fail.
      dm=$(readlink -f "$dev" 2>/dev/null)
      [ -z "$dm" ] && dm=$dev
      dm=${dm##*/}
      [ -f /sys/block/"$dm"/force_ro ] && echo 0 > /sys/block/"$dm"/force_ro 2>/dev/null
      ;;
  esac

  mount -o rw,remount "$mnt" 2>/dev/null
  is_writable "$mnt" && return 0
  [ -n "$dev" ] && mount -o rw,remount "$dev" "$mnt" 2>/dev/null
  is_writable "$mnt"
}

# Mount a partition read-write. Tries the existing mount, then the fstab entry,
# then the block device resolved by name and slot.
mount_part() {
  mnt=$1
  name=${mnt##*/}

  if grep -q " $mnt " /proc/mounts 2>/dev/null; then
    make_rw "$mnt" && return 0
  fi

  mkdir -p "$mnt" 2>/dev/null
  if mount -o rw "$mnt" 2>/dev/null || mount "$mnt" 2>/dev/null; then
    MOUNTED_BY_US="$MOUNTED_BY_US $mnt"
    make_rw "$mnt" && return 0
  fi

  dev=$(find_block "$name") || return 1
  blockdev --setrw "$dev" 2>/dev/null
  if mount -o rw -t auto "$dev" "$mnt" 2>/dev/null; then
    MOUNTED_BY_US="$MOUNTED_BY_US $mnt"
    is_writable "$mnt" && return 0
  fi
  return 1
}

# Why mount_part gave up on $1. Several unrelated causes all used to surface
# as "cannot mount /product", which told nobody anything.
mount_reason() {
  mnt=$1
  name=${mnt##*/}
  fstype=$(grep " $mnt " /proc/mounts 2>/dev/null | head -n1 | awk '{print $3}')

  case "$fstype" in
    erofs|squashfs)
      echo "it is $fstype, a read-only format; this ROM cannot be changed from recovery"
      return 0 ;;
  esac
  if [ -n "$fstype" ]; then
    echo "mounted $fstype but it would not accept a write"
    return 0
  fi

  dev=$(find_block "$name")
  if [ -n "$dev" ]; then
    echo "$dev exists but would not mount"
  else
    echo "no block device named $name under $(block_paths | tr '\n' ' ')"
  fi
  return 0
}

# Clear factory reset protection.
#
# Opt-in: installer.sh only calls this when the zip carries installer/wipe-frp.
# Prefers the recovery's own wipe-frp binary and falls back to writing the FRP
# block directly, the way the LineageOS tool does. Never fails the install --
# a gapps flash should still finish if FRP cannot be cleared.
wipe_frp() {
  block=${1:-$(getprop ro.frp.pst 2>/dev/null)}
  if [ -z "$block" ]; then
    ui_print "  . no FRP partition on this device"
    return 0
  fi
  if [ ! -b "$block" ]; then
    ui_print "  . FRP: $block is not a block device, skipping"
    return 0
  fi
  blockdev --setrw "$block" 2>/dev/null

  # The vendor's own tool is tested against this device; use it when present.
  for tool in wipe-frp /system/bin/wipe-frp /vendor/bin/wipe-frp /sbin/wipe-frp /bin/wipe-frp; do
    if command -v "$tool" >/dev/null 2>&1; then
      if "$tool" "$block" >/dev/null 2>&1; then
        ui_print "  . FRP cleared"
        return 0
      fi
      break
    fi
  done

  frp_wipe_block "$block"
}

# Zero the FRP structure and rewrite its digest, matching LineageOS wipe-frp.
frp_wipe_block() {
  block=$1
  size=$(blockdev --getsize64 "$block" 2>/dev/null)
  case "$size" in
    ''|*[!0-9]*) ui_print "  . FRP: cannot read the size of $block, skipping"; return 0 ;;
  esac
  # An FRP partition is small. Refuse anything outside a sane range so a
  # mis-set ro.frp.pst cannot zero a real partition.
  if [ "$size" -lt 12000 ] || [ "$size" -gt 16777216 ]; then
    ui_print "  . FRP: $block is $size bytes, outside the expected range; skipping"
    return 0
  fi

  digest_size=32
  oem_off=$((size - 1))
  cred_size=1000;  cred_off=$((oem_off - cred_size))
  test_size=10000; test_off=$((cred_off - test_size))
  secret_size=32;  secret_off=$((test_off - secret_size))
  magic_size=8;    magic_off=$((secret_off - magic_size))

  # conv=notrunc: harmless on a block device, and it stops dd truncating a
  # regular file, which is what the tests run against.
  dd if=/dev/zero of="$block" bs=1 seek=0 count="$digest_size" conv=notrunc 2>/dev/null
  dd if=/dev/zero of="$block" bs=1 seek="$cred_off" count="$cred_size" conv=notrunc 2>/dev/null
  dd if=/dev/zero of="$block" bs=1 seek="$secret_off" count="$secret_size" conv=notrunc 2>/dev/null
  printf '\xDA\xC2\xFC\xCD\xB9\x1B\x09\x88' |
    dd of="$block" bs=1 seek="$magic_off" count="$magic_size" conv=notrunc 2>/dev/null

  # Rewrite the digest over the whole, now-modified block. No xxd in the
  # bundled busybox, so turn the hex into printf \x escapes by hand.
  # shellcheck disable=SC2046  # deliberately split "<hex>  <file>" to take the hex
  set -- $(sha256sum "$block" 2>/dev/null)
  hex=$1
  if [ ${#hex} -ne 64 ]; then
    ui_print "  . FRP: could not compute the digest; left cleared without it"
    return 0
  fi
  esc=$(printf '%s' "$hex" | sed 's/\(..\)/\\x\1/g')
  # shellcheck disable=SC2059  # esc is our own hex, not user input
  printf "$esc" | dd of="$block" bs=1 seek=0 count="$digest_size" conv=notrunc 2>/dev/null
  ui_print "  . FRP cleared"
  return 0
}

# Bytes used by a file or directory tree. Estimates what a removal will free.
du_bytes() {
  du -s -k "$1" 2>/dev/null | awk '{print $1 * 1024; exit}'
}

# Free bytes on the filesystem holding $1.
free_bytes() {
  # $(NF-2) rather than $4: busybox df puts a long device name on a line of
  # its own and wraps the columns onto the next, after which the available
  # column is the third field, not the fourth. Counting back from the end is
  # right either way -- the last three fields are always available, use% and
  # mount point.
  #
  # Reading $4 off a wrapped line picks up the use percentage instead, which
  # on a real device reported 22 KB free for a partition with 2.4 GiB.
  df -k "$1" 2>/dev/null | tail -n1 | awk '{print $(NF-2) * 1024}'
}

# Grow a logical partition by <bytes> when the ROM left no slack.
# Every variable here is prefixed. A shell function shares the caller's scope,
# so the plain names this used to use -- need, target -- silently overwrote the
# space check's own, and an abort then reported a negative size.
grow_part() {
  _gp_mnt=$1; _gp_need=$2
  _gp_dev=$(block_for "$_gp_mnt")
  case "$_gp_dev" in
    /dev/block/dm-*) ;;
    *) return 1 ;;
  esac
  command -v resize2fs >/dev/null 2>&1 || return 1
  _gp_name=$(basename "$_gp_mnt")
  _gp_cur=$(blockdev --getsize64 "$_gp_dev" 2>/dev/null) || return 1
  _gp_target=$(add_bytes "$_gp_cur" "$(add_bytes "$_gp_need" 33554432)")
  if command -v lptools >/dev/null 2>&1; then
    lptools unmap "$_gp_name" >/dev/null 2>&1
    lptools resize "$_gp_name" "$_gp_target" >/dev/null 2>&1 || return 1
    lptools map "$_gp_name" >/dev/null 2>&1
    _gp_dev=$(block_for "$_gp_mnt")
  fi
  resize2fs "$_gp_dev" >/dev/null 2>&1 || return 1
  return 0
}

set_meta() {
  path=$1; mode=$2; ctx=$3
  chmod "$mode" "$path" 2>/dev/null
  chown 0:0 "$path" 2>/dev/null
  [ -n "$ctx" ] && chcon "$ctx" "$path" 2>/dev/null
  return 0
}

cleanup() {
  for m in $MOUNTED_BY_US; do
    umount "$m" 2>/dev/null
  done
  rm -rf "$TMP"
}

# shellcheck disable=SC2034  # the guard the sourcing scripts test
UTIL_LOADED=1
