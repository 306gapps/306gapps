#!/sbin/sh
# 306gapps recovery installer.
#
# Inputs unpacked to $TMP/installer:
#   release.txt   key=value metadata for display
#   files.list    path<TAB>mode<TAB>context<TAB>size, one per install target
#   removals.txt  partition-relative paths to delete before installing
#   props.txt     key=value build properties to append
#   addon.d.sh    survival script, installed when the ROM supports addon.d

# A test harness may preload stubs for the device-touching helpers.
[ "${UTIL_LOADED:-0}" = 1 ] || . "$TMP/installer/util.sh"

# PREFIX lets the installer run against a tree other than the live root, which
# is how the installer is exercised off-device. Empty in production.
PREFIX="${GAPPS_PREFIX:-}"

MOUNTED_BY_US=""
LIST="$TMP/installer/files.list"
VERBOSE=$(sed -n 's/^verbose=//p' "$TMP/installer/release.txt")

NAME=$(sed -n 's/^name=//p' "$TMP/installer/release.txt")
RELEASE=$(sed -n 's/^release=//p' "$TMP/installer/release.txt")
ANDROID=$(sed -n 's/^android=//p' "$TMP/installer/release.txt")
APILEVEL=$(sed -n 's/^api=//p' "$TMP/installer/release.txt")
PKGCOUNT=$(sed -n 's/^packages=//p' "$TMP/installer/release.txt")

ui_print "========================================="
ui_print " $NAME"
ui_print " release $RELEASE"
ui_print " Android $ANDROID (API $APILEVEL), $PKGCOUNT packages"
ui_print "========================================="
ui_print " "
if [ -n "$BUSYBOX" ]; then
  log "using bundled $($BUSYBOX 2>&1 | head -n1)"
else
  ui_print "- no bundled busybox; using the recovery's own tools"
fi

# ---- verify the ROM matches ------------------------------------------------

unlock_blocks
mount_part "$PREFIX/system" || mount_part "$PREFIX/system_root" || abort "cannot mount /system"
BUILDPROP=""
for p in "$PREFIX/system/system/build.prop" "$PREFIX/system/build.prop"; do
  [ -f "$p" ] && BUILDPROP="$p" && break
done
[ -z "$BUILDPROP" ] && abort "no build.prop found - is a ROM installed?"

SYSROOT=$(dirname "$BUILDPROP")
DEVICE_API=$(sed -n 's/^ro.build.version.sdk=//p' "$BUILDPROP" | head -n1)
if [ -n "$DEVICE_API" ] && [ "$DEVICE_API" != "$APILEVEL" ]; then
  abort "package targets API $APILEVEL but this ROM is API $DEVICE_API"
fi
ui_print "- ROM matches API $APILEVEL"

# ---- mount every partition the package writes to ---------------------------

PARTS=$(awk -F'\t' '{split($1,a,"/"); print a[1]}' "$LIST" | sort -u)
for part in $PARTS; do
  case "$part" in
    system) target="$SYSROOT" ;;
    *)
      # /product and /system_ext are often symlinks into /system on older ROMs.
      if [ -d "$SYSROOT/$part" ] && ! grep -q " /$part " /proc/mounts 2>/dev/null; then
        target="$SYSROOT/$part"
      else
        mount_part "$PREFIX/$part" || abort "cannot mount /$part"
        target="$PREFIX/$part"
      fi
      ;;
  esac
  eval "ROOT_$part=\"\$target\""
  log "/$part -> $target"
done

# ---- space check -----------------------------------------------------------

for part in $PARTS; do
  eval "target=\$ROOT_$part"
  need=$(awk -F'\t' -v p="$part/" 'index($1,p)==1 {s+=$4} END {print s+0}' "$LIST")
  free=$(free_bytes "$target")
  ui_print "- /$part needs $(human "$need"), has $(human "$free") free"
  if [ "$free" -lt "$need" ]; then
    ui_print "  trying to grow /$part..."
    if grow_part "$PREFIX/$part" "$((need - free))"; then
      free=$(free_bytes "$target")
      ui_print "  grew to $(human "$free") free"
    fi
  fi
  [ "$free" -lt "$need" ] && abort "not enough space on /$part: need $(human "$need"), have $(human "$free")"
done

# ---- remove superseded AOSP packages ---------------------------------------

if [ -s "$TMP/installer/removals.txt" ]; then
  ui_print "- removing superseded apps"
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    part=${rel%%/*}
    eval "target=\$ROOT_$part"
    [ -z "$target" ] && continue
    victim="$target/${rel#*/}"
    if [ -e "$victim" ]; then
      log "rm $rel"
      rm -rf "$victim"
    fi
  done < "$TMP/installer/removals.txt"
fi

# ---- install ---------------------------------------------------------------

TOTAL=$(wc -l < "$LIST" | tr -d ' ')
N=0
ui_print "- installing $TOTAL files"

# Digest verification is far stronger than a size check, and the bundled
# busybox provides sha256sum even when the recovery does not.
HAVE_SHA=0
command -v sha256sum >/dev/null 2>&1 && HAVE_SHA=1

while IFS="$(printf '\t')" read -r rel mode ctx size link; do
  [ -z "$rel" ] && continue
  N=$((N + 1))
  part=${rel%%/*}
  eval "target=\$ROOT_$part"
  dest="$target/${rel#*/}"

  mkdir -p "$(dirname "$dest")" || abort "cannot create $(dirname "$dest")"

  # A fifth column names a link target; such records carry no payload.
  if [ -n "$link" ]; then
    rm -rf "$dest"
    ln -s "$link" "$dest" || abort "cannot link $rel -> $link"
    log "link $rel -> $link"
    continue
  fi

  # Stream out of the zip onto the destination filesystem so the install never
  # needs room for a second copy, and never fills tmpfs.
  scratch="$target/.306gapps.part"
  if ! unzip -p "$ZIPFILE" "files/$rel" > "$scratch" 2>/dev/null; then
    rm -f "$scratch"
    abort "failed to extract $rel"
  fi
  actual=$(wc -c < "$scratch" | tr -d ' ')
  if [ "$actual" != "$size" ]; then
    rm -f "$scratch"
    abort "size mismatch for $rel: expected $size, got $actual"
  fi
  if [ "$HAVE_SHA" = 1 ]; then
    want=$(sed -n "s|^$rel  ||p" "$TMP/installer/digests.txt" 2>/dev/null | head -n1)
    if [ -n "$want" ]; then
      got=$(sha256sum "$scratch" | cut -d" " -f1)
      if [ "$got" != "$want" ]; then
        rm -f "$scratch"
        abort "corrupt payload for $rel"
      fi
    fi
  fi
  mv -f "$scratch" "$dest" || abort "cannot write $dest"
  set_meta "$dest" "$mode" "$ctx"

  case $((N * 100 / TOTAL)) in
    25|50|75) ui_print "  $((N * 100 / TOTAL))% ($N/$TOTAL)" ;;
  esac
done < "$LIST"

ui_print "  100% ($TOTAL/$TOTAL)"

# ---- build properties ------------------------------------------------------

if [ -s "$TMP/installer/props.txt" ]; then
  ui_print "- applying build properties"
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    key=${line%%=*}
    grep -q "^$key=" "$BUILDPROP" && sed -i "/^$key=/d" "$BUILDPROP"
    echo "$line" >> "$BUILDPROP"
  done < "$TMP/installer/props.txt"
fi

# ---- addon.d survival ------------------------------------------------------

if [ -d "$SYSROOT/addon.d" ]; then
  ui_print "- installing addon.d survival script"
  cp -f "$TMP/installer/addon.d.sh" "$SYSROOT/addon.d/69-306gapps.sh"
  set_meta "$SYSROOT/addon.d/69-306gapps.sh" 0755 "u:object_r:system_file:s0"
else
  ui_print "- ROM has no addon.d; gapps will not survive a dirty flash"
fi

# ---- record what we installed ----------------------------------------------

mkdir -p "$SYSROOT/etc/306gapps"
cp -f "$LIST" "$SYSROOT/etc/306gapps/files.list"
cp -f "$TMP/installer/release.txt" "$SYSROOT/etc/306gapps/release.txt"
set_meta "$SYSROOT/etc/306gapps/files.list" 0644 "u:object_r:system_file:s0"
set_meta "$SYSROOT/etc/306gapps/release.txt" 0644 "u:object_r:system_file:s0"

ui_print " "
ui_print "- done. Wipe cache/dalvik before rebooting."
cleanup
exit 0
