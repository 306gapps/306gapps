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
  while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    case "$entry" in
      */*)
        case "$entry" in
          system/*) set -- "$MODPATH/$entry" ;;
          *)        set -- "$MODPATH/system/$entry" ;;
        esac
        ;;
      *)
        # A bare name masks every app location on every partition; a marker
        # over a directory that does not exist is harmless.
        set -- "$MODPATH/system/app/$entry" "$MODPATH/system/priv-app/$entry" \
               "$MODPATH/system/product/app/$entry" "$MODPATH/system/product/priv-app/$entry" \
               "$MODPATH/system/system_ext/app/$entry" "$MODPATH/system/system_ext/priv-app/$entry"
        ;;
    esac
    for dir in "$@"; do
      mkdir -p "$dir"
      touch "$dir/.replace"
    done
  done < "$MODPATH/removals.txt"
fi

ui_print "- setting permissions"
set_perm_recursive "$MODPATH/system" 0 0 0755 0644
# Piped into read rather than iterated as $(find ...): word splitting would
# break on a path containing a space, and the set_perm_recursive would then be
# applied to the wrong directory or none at all.
for d in app priv-app; do
  find "$MODPATH/system" -type d -name "$d" 2>/dev/null | while IFS= read -r p; do
    set_perm_recursive "$p" 0 0 0755 0644
  done
done

ui_print "- done, reboot to apply"
