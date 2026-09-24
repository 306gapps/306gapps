#!/sbin/sh
# Runs inside the manager with $MODPATH pointing at the unpacked module.

APILEVEL=$(sed -n 's/^api=//p' "$MODPATH/release.txt")
NAME=$(sed -n 's/^name=//p' "$MODPATH/release.txt")
RELEASE=$(sed -n 's/^release=//p' "$MODPATH/release.txt")
ANDROID=$(sed -n 's/^android=//p' "$MODPATH/release.txt")
PKGCOUNT=$(sed -n 's/^packages=//p' "$MODPATH/release.txt")

ui_print "- $NAME $RELEASE"
ui_print "- Android $ANDROID (API $APILEVEL), $PKGCOUNT packages"

DEVICE_API=$(getprop ro.build.version.sdk)
if [ -n "$DEVICE_API" ] && [ "$DEVICE_API" != "$APILEVEL" ]; then
  abort "! package targets API $APILEVEL but this device is API $DEVICE_API"
fi

# Superseded AOSP apps are hidden with an opaque replace marker rather than
# deleted, so disabling the module restores the stock ROM exactly.
if [ -s "$MODPATH/removals.txt" ]; then
  ui_print "- masking superseded apps"
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    case "$rel" in
      system/*) dir="$MODPATH/$rel" ;;
      *)        dir="$MODPATH/system/$rel" ;;
    esac
    mkdir -p "$dir"
    touch "$dir/.replace"
  done < "$MODPATH/removals.txt"
fi

ui_print "- setting permissions"
set_perm_recursive "$MODPATH/system" 0 0 0755 0644
for d in app priv-app; do
  for p in $(find "$MODPATH/system" -type d -name "$d" 2>/dev/null); do
    set_perm_recursive "$p" 0 0 0755 0644
  done
done

ui_print "- done, reboot to apply"
