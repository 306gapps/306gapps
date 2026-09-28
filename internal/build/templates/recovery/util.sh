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
  echo "$slot"
}

# Block device path prefix: dynamic partitions live under the device mapper.
block_path() {
  if [ -d /dev/block/mapper ]; then
    echo /dev/block/mapper
  elif [ -d /dev/block/bootdevice/by-name ]; then
    echo /dev/block/bootdevice/by-name
  else
    find /dev/block/platform -type d -name by-name 2>/dev/null | head -n1
  fi
}

# Locate the block device for a partition by name, honouring the active slot.
# Needed when recovery has not already mounted the partition for us.
find_block() {
  name=$1
  base=$(block_path)
  [ -z "$base" ] && return 1
  slot=$(find_slot)

  for candidate in "$base/$name$slot" "$base/$name"; do
    [ -b "$candidate" ] && echo "$candidate" && return 0
  done

  # System-as-root devices may present /system under another name.
  if [ "$name" = "system" ]; then
    for candidate in "$base/system$slot" "$base/system_root$slot"; do
      [ -b "$candidate" ] && echo "$candidate" && return 0
    done
  fi
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
      dm=$(basename "$dev")
      [ -w /sys/block/"$dm"/force_ro ] && echo 0 > /sys/block/"$dm"/force_ro 2>/dev/null
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

# shellcheck disable=SC2034  # the guard the sourcing scripts test
UTIL_LOADED=1
