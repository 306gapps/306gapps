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

# Make a mounted partition writable, handling dynamic (super) partitions where
# the underlying dm device is read-only until explicitly flipped.
make_rw() {
  mnt=$1
  dev=$(block_for "$mnt")
  [ -z "$dev" ] && return 1

  case "$dev" in
    /dev/block/dm-*)
      # Logical partition: clear the device-mapper read-only flag first.
      blockdev --setrw "$dev" 2>/dev/null
      dm=$(basename "$dev")
      [ -w /sys/block/"$dm"/force_ro ] && echo 0 > /sys/block/"$dm"/force_ro 2>/dev/null
      ;;
  esac

  mount -o rw,remount "$mnt" 2>/dev/null && return 0
  mount -o rw,remount "$dev" "$mnt" 2>/dev/null && return 0
  return 1
}

# Mount a partition read-write, trying the mountpoint then the fstab entry.
mount_part() {
  mnt=$1
  if grep -q " $mnt " /proc/mounts 2>/dev/null; then
    make_rw "$mnt" || return 1
    MOUNTED_BY_US="$MOUNTED_BY_US"
    return 0
  fi
  mkdir -p "$mnt" 2>/dev/null
  mount "$mnt" 2>/dev/null || mount -o rw "$mnt" 2>/dev/null || return 1
  make_rw "$mnt" || return 1
  MOUNTED_BY_US="$MOUNTED_BY_US $mnt"
  return 0
}

# Free bytes on the filesystem holding $1.
free_bytes() {
  df -k "$1" 2>/dev/null | tail -n1 | awk '{print $4 * 1024}'
}

# Grow a logical partition by <bytes> when the ROM left no slack.
grow_part() {
  mnt=$1; need=$2
  dev=$(block_for "$mnt")
  case "$dev" in
    /dev/block/dm-*) ;;
    *) return 1 ;;
  esac
  command -v resize2fs >/dev/null 2>&1 || return 1
  name=$(basename "$mnt")
  cur=$(blockdev --getsize64 "$dev" 2>/dev/null) || return 1
  target=$((cur + need + 33554432))
  if command -v lptools >/dev/null 2>&1; then
    lptools unmap "$name" >/dev/null 2>&1
    lptools resize "$name" "$target" >/dev/null 2>&1 || return 1
    lptools map "$name" >/dev/null 2>&1
    dev=$(block_for "$mnt")
  fi
  resize2fs "$dev" >/dev/null 2>&1 || return 1
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

UTIL_LOADED=1
