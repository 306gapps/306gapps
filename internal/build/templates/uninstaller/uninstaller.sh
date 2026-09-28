#!/sbin/sh
# Removes a 306gapps install, using the record the installer left behind.
#
# This deletes only what we installed. Apps the install removed to make room
# are not restored here -- dirty-flash the ROM first and they come back with
# it, then run this to take the Google apps back out.

[ "${UTIL_LOADED:-0}" = 1 ] || . "$TMP/installer/util.sh"

PREFIX="${GAPPS_PREFIX:-}"
MOUNTED_BY_US=""
VERBOSE=0

ui_print "========================================="
ui_print " 306gapps uninstaller"
ui_print "========================================="
ui_print " "

unlock_blocks
mount_part "$PREFIX/system" || mount_part "$PREFIX/system_root" || abort "cannot mount /system"

SYSROOT=""
for p in "$PREFIX/system/system" "$PREFIX/system"; do
  [ -f "$p/build.prop" ] && SYSROOT="$p" && break
done
[ -z "$SYSROOT" ] && abort "no build.prop found - is a ROM installed?"

RECORD="$SYSROOT/etc/306gapps/files.list"
if [ ! -f "$RECORD" ]; then
  ui_print "- nothing to uninstall: no 306gapps install recorded"
  ui_print "  (if you just dirty-flashed, addon.d may not have restored yet)"
  cleanup
  exit 0
fi

RELEASE=$(sed -n 's/^release=//p' "$SYSROOT/etc/306gapps/release.txt" 2>/dev/null)
ui_print "- found install: ${RELEASE:-unknown release}"

# Mount every partition the record mentions.
PARTS=$(awk -F'\t' '{split($1,a,"/"); print a[1]}' "$RECORD" | sort -u)
for part in $PARTS; do
  case "$part" in
    system) target="$SYSROOT" ;;
    *)
      if [ -d "$SYSROOT/$part" ] && ! grep -q " /$part " /proc/mounts 2>/dev/null; then
        target="$SYSROOT/$part"
      else
        mount_part "$PREFIX/$part" || abort "cannot mount /$part"
        target="$PREFIX/$part"
      fi
      ;;
  esac
  eval "ROOT_$part=\"\$target\""
done

TOTAL=$(wc -l < "$RECORD" | tr -d ' ')
ui_print "- removing $TOTAL files"

N=0
while IFS="$(printf '\t')" read -r rel mode ctx size link; do
  [ -z "$rel" ] && continue
  part=${rel%%/*}
  eval "target=\$ROOT_$part"
  [ -z "$target" ] && continue
  victim="$target/${rel#*/}"
  if [ -e "$victim" ] || [ -L "$victim" ]; then
    rm -rf "$victim"
    N=$((N + 1))
  fi
done < "$RECORD"
ui_print "  removed $N"

# Prune directories the install created, deepest first, stopping at anything
# the ROM still uses.
ui_print "- pruning empty directories"
awk -F'\t' '{print $1}' "$RECORD" | while IFS= read -r rel; do
  part=${rel%%/*}
  eval "target=\$ROOT_$part"
  [ -z "$target" ] && continue
  dir=$(dirname "$target/${rel#*/}")
  while [ "$dir" != "$target" ] && [ "$dir" != "/" ] && [ -d "$dir" ]; do
    rmdir "$dir" 2>/dev/null || break
    dir=$(dirname "$dir")
  done
done

# The survival script must go, or the next dirty flash restores everything.
if [ -f "$SYSROOT/addon.d/69-306gapps.sh" ]; then
  ui_print "- removing the addon.d survival script"
  rm -f "$SYSROOT/addon.d/69-306gapps.sh"
fi

rm -rf "$SYSROOT/etc/306gapps"

ui_print " "
ui_print "- done. Apps that were replaced come back when you flash the ROM."
ui_print "- wipe cache/dalvik before rebooting."
cleanup
exit 0
