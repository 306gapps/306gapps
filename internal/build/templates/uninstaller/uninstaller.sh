#!/sbin/sh
# Removes a 306gapps install, using the record the installer left behind.
#
# This deletes only what we installed. Apps the install removed to make room
# are not restored here -- dirty-flash the ROM first and they come back with
# it, then run this to take the Google apps back out.

[ "${UTIL_LOADED:-0}" = 1 ] || . "$TMP/installer/util.sh"

PREFIX="${GAPPS_PREFIX:-}"
# shellcheck disable=SC2034  # appended to and read by util.sh
MOUNTED_BY_US=""
# shellcheck disable=SC2034  # read by log() in util.sh
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
# Only rel is wanted; the rest of the record is read to discard it.
# shellcheck disable=SC2034
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

# ---- app data --------------------------------------------------------------
#
# Removing the system apk leaves the app's data, any update the Play Store
# installed to /data/app, and its compiled profiles. Left behind, a later
# reinstall inherits stale state and an orphaned /data/app update shadows
# nothing at all.
PKGLIST="$SYSROOT/etc/306gapps/packages.txt"
if [ -s "$PKGLIST" ]; then
  if ! grep -q " /data " /proc/mounts 2>/dev/null; then
    # Both may fail -- /data is often encrypted in recovery, and the caller
    # may be running under set -e -- so neither is allowed to be fatal.
    mount "$PREFIX/data" 2>/dev/null || mount -o rw "$PREFIX/data" 2>/dev/null || true
  fi

  if [ -d "$PREFIX/data/data" ] || [ -d "$PREFIX/data/app" ]; then
    ui_print "- clearing app data"
    CLEARED=0
    # drop_data removes one path if it is there. Written as a function with an
    # explicit test rather than an && chain, which returns false when the path
    # is absent and would abort a caller running under set -e.
    drop_data() {
      if [ -e "$1" ]; then
        rm -rf "$1"
        CLEARED=$((CLEARED + 1))
      fi
      return 0
    }

    while IFS= read -r pkg; do
      [ -z "$pkg" ] && continue
      drop_data "$PREFIX/data/data/$pkg"
      drop_data "$PREFIX/data/misc/profiles/ref/$pkg"

      # Per-user copies, one directory per user id.
      for base in "$PREFIX/data/user" "$PREFIX/data/user_de" \
                  "$PREFIX/data/misc/profiles/cur"; do
        if [ -d "$base" ]; then
          for u in "$base"/*; do
            drop_data "$u/$pkg"
          done
        fi
      done

      # Updates the Play Store installed over the system app. Modern Android
      # nests these under a random directory, so look one level down too.
      for d in "$PREFIX/data/app/$pkg"-* "$PREFIX/data/app"/*/"$pkg"-*; do
        drop_data "$d"
      done
    done < "$PKGLIST"
    ui_print "  cleared $CLEARED"
  else
    ui_print "- /data is not readable (encrypted?); app data left in place"
    ui_print "  format data, or clear storage per app once booted"
  fi
fi

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
