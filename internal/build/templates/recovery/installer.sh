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

# shellcheck disable=SC2034  # appended to and read by util.sh
MOUNTED_BY_US=""
LIST="$TMP/installer/files.list"
# shellcheck disable=SC2034  # read by log() in util.sh
VERBOSE=$(sed -n 's/^verbose=//p' "$TMP/installer/release.txt")

NAME=$(sed -n 's/^name=//p' "$TMP/installer/release.txt")
RELEASE=$(sed -n 's/^release=//p' "$TMP/installer/release.txt")
ANDROID=$(sed -n 's/^android=//p' "$TMP/installer/release.txt")
APILEVEL=$(sed -n 's/^api=//p' "$TMP/installer/release.txt")
PKGCOUNT=$(sed -n 's/^packages=//p' "$TMP/installer/release.txt")

# Payloads may be xz-compressed (installer/compression). Decompression needs
# unxz, which the bundled busybox provides; fail early and clearly if it is
# missing rather than midway through with a size-mismatch.
COMPRESSION=$(sed -n '1p' "$TMP/installer/compression" 2>/dev/null)
if [ "$COMPRESSION" = xz ] && ! command -v unxz >/dev/null 2>&1; then
  abort "this package is xz-compressed but unxz is unavailable in this recovery"
fi

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
mount_part "$PREFIX/system" || mount_part "$PREFIX/system_root" ||
  abort "cannot mount /system: $(mount_reason "$PREFIX/system")"
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

# A removal can name a partition the payload never writes to -- and a bare
# name means every partition -- so those have to be mounted too.
if [ -s "$TMP/installer/removals.txt" ]; then
  if grep -qv '/' "$TMP/installer/removals.txt"; then
    PARTS=$(printf '%s\nsystem\nsystem_ext\nproduct\n' "$PARTS" | sort -u)
  else
    # A brace group rather than process substitution: recovery's shell is
    # ash, which has no <(...), and neither does dash.
    PARTS=$({ awk -F/ 'NF>1 {print $1}' "$TMP/installer/removals.txt"
              echo "$PARTS"; } | sort -u)
  fi
fi
PARTS=$(echo "$PARTS" | grep -v '^$')
for part in $PARTS; do
  case "$part" in
    system) target="$SYSROOT" ;;
    *)
      # Take the real partition whenever the device has one. On anything
      # modern /product and /system_ext are separate dynamic partitions, and
      # /system/system/product is a symlink to their mount point -- which in
      # recovery is an empty directory on the ramdisk. Testing that path with
      # -d succeeds, so preferring it measured the ramdisk (0 bytes free) and
      # would have written the payload into RAM.
      #
      # Only fall back to a directory inside /system when it is genuinely
      # populated, which is what an older single-partition ROM looks like.
      if mount_part "$PREFIX/$part"; then
        target="$PREFIX/$part"
      elif [ -d "$SYSROOT/$part" ] && [ -n "$(ls -A "$SYSROOT/$part" 2>/dev/null)" ]; then
        target="$SYSROOT/$part"
      else
        abort "cannot mount /$part: $(mount_reason "$PREFIX/$part")"
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
  # lt, not [ -lt: sizes here routinely exceed what recovery's shell can
  # compare. See util.sh.
  if lt "$free" "$need"; then
    ui_print "  trying to grow /$part..."
    if grow_part "$PREFIX/$part" "$(sub_bytes "$need" "$free")"; then
      free=$(free_bytes "$target")
      ui_print "  grew to $(human "$free") free"
    fi
  fi
  lt "$free" "$need" && abort "not enough space on /$part: need $(human "$need"), have $(human "$free")"
done

# ---- remove superseded AOSP packages ---------------------------------------

# drop_path removes one resolved victim, if it is there.
drop_path() {
  part=$1; rest=$2
  eval "target=\$ROOT_$part"
  [ -z "$target" ] && return 0
  victim="$target/$rest"
  if [ -e "$victim" ]; then
    log "rm $part/$rest"
    rm -rf "$victim"
    REMOVED=$((REMOVED + 1))
  fi
}

if [ -s "$TMP/installer/removals.txt" ]; then
  ui_print "- removing superseded apps"
  REMOVED=0
  while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    case "$entry" in
      */*)
        # An exact partition-relative path.
        drop_path "${entry%%/*}" "${entry#*/}"
        ;;
      *)
        # A bare name: look everywhere an app can live. ROMs disagree about
        # which partition holds a given app, so the name is what identifies it.
        for part in system system_ext product; do
          for dir in app priv-app; do
            drop_path "$part" "$dir/$entry"
          done
        done
        ;;
    esac
  done < "$TMP/installer/removals.txt"
  ui_print "  removed $REMOVED"
fi

# ---- install ---------------------------------------------------------------

TOTAL=$(wc -l < "$LIST" | tr -d ' ')
N=0
ui_print "- installing $TOTAL files"

# Digest verification is far stronger than a size check, and the bundled
# busybox provides sha256sum even when the recovery does not.
HAVE_SHA=0
command -v sha256sum >/dev/null 2>&1 && HAVE_SHA=1

# Each package is announced the first time one of its files comes up, so the
# screen says what is being written rather than sitting silent between
# percentages. files.list is path-sorted and a package's files share a
# directory, so in practice each name is announced once and stays put.
#
# Delimited with tabs rather than spaces: package names contain spaces, and
# "Google Calendar" would otherwise match inside "Google Calendar Sync".
# A tab cannot appear in a name -- files.list is tab-separated.
TAB=$(printf '\t')
SEEN_PKGS="$TAB"

while IFS="$(printf '\t')" read -r rel mode ctx size owner link; do
  [ -z "$rel" ] && continue
  N=$((N + 1))

  if [ -n "$owner" ]; then
    case "$SEEN_PKGS" in
      *"$TAB$owner$TAB"*) ;;
      *)
        ui_print "  $owner"
        SEEN_PKGS="$SEEN_PKGS$owner$TAB"
        ;;
    esac
  fi
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
  if [ "$COMPRESSION" = xz ]; then
    # A failed unzip or unxz leaves a short/garbage file; the size and digest
    # checks below catch it either way.
    unzip -p "$ZIPFILE" "files/$rel.xz" 2>/dev/null | unxz > "$scratch" 2>/dev/null
  else
    unzip -p "$ZIPFILE" "files/$rel" > "$scratch" 2>/dev/null
  fi
  if [ ! -f "$scratch" ]; then
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

done < "$LIST"

ui_print "  $TOTAL files written"

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
# The package ids an uninstall needs to clear app data with.
cp -f "$TMP/installer/packages.txt" "$SYSROOT/etc/306gapps/packages.txt" 2>/dev/null
for f in files.list release.txt packages.txt; do
  [ -f "$SYSROOT/etc/306gapps/$f" ] &&
    set_meta "$SYSROOT/etc/306gapps/$f" 0644 "u:object_r:system_file:s0"
done

# ---- clear factory reset protection (opt-in) -------------------------------

if [ -f "$TMP/installer/wipe-frp" ]; then
  ui_print "- clearing factory reset protection"
  wipe_frp
fi

# ---- wipe the caches -------------------------------------------------------

# Newly installed apks have to be recompiled, and a stale dalvik cache is the
# usual cause of a bootloop after flashing gapps. Doing it here is one less
# step to forget, and it is exactly what the old "wipe cache/dalvik before
# rebooting" note was asking the user to do by hand.
ui_print "- wiping dalvik cache"
for d in /data/dalvik-cache /data/resource-cache /cache/dalvik-cache; do
  [ -d "$d" ] && rm -rf "${d:?}"/* 2>/dev/null
done
if mount_part "$PREFIX/data" 2>/dev/null; then
  rm -rf "$PREFIX/data/dalvik-cache"/* 2>/dev/null
  rm -rf "$PREFIX/data/resource-cache"/* 2>/dev/null
fi
if mount_part "$PREFIX/cache" 2>/dev/null; then
  rm -rf "$PREFIX/cache/dalvik-cache"/* 2>/dev/null
fi

ui_print " "
ui_print "- done. First boot will take a few minutes while apps recompile."
cleanup
exit 0
