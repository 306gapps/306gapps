#!/usr/bin/env bash
# Builds a recovery zip from the fixture, installs it into a fake ROM tree and
# asserts the result. Usage: run.sh <306gapps-binary> <fixture-dir>
set -euo pipefail

BIN=$1
FIXTURE=$2
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail=0
check() {
  if eval "$2"; then
    echo "  ok   $1"
  else
    echo "  FAIL $1"
    fail=1
  fi
}

# Rewrite one digest so the installer must reject the payload it names.
corrupt_payload_is_refused() {
  local bad="$WORK/bad.zip" out="$WORK/badlog"
  rm -rf "$WORK/rebuild"; mkdir -p "$WORK/rebuild"
  ( cd "$WORK/rebuild" && unzip -q -o "$ZIP" )
  sed -i 's|^product/priv-app/Velvet/Velvet.apk  .*|product/priv-app/Velvet/Velvet.apk  '"$(printf 'f%.0s' $(seq 64))"'|' \
    "$WORK/rebuild/installer/digests.txt"
  ( cd "$WORK/rebuild" && zip -qr "$bad" . )

  local rom2="$WORK/rom2"
  rm -rf "$rom2"; mkdir -p "$rom2/system/system/addon.d"
  printf 'ro.build.version.sdk=36\n' > "$rom2/system/system/build.prop"
  rm -rf "$WORK/tmp2"; mkdir -p "$WORK/tmp2"
  unzip -q -o "$bad" 'installer/*' -d "$WORK/tmp2"
  if busybox ash "$HERE/harness.sh" "$WORK/tmp2" "$bad" "$rom2" > "$out" 2>&1; then
    return 1
  fi
  grep -q "corrupt payload" "$out"
}

ZIP="$WORK/gapps.zip"
GAPPS_CACHE="$WORK/cache" "$BIN" build -source "$FIXTURE" \
  -packages gsa,photos,dialer-google -out "$ZIP" >/dev/null 2>&1

# A fake ROM: system + product + system_ext, with a matching API level and an
# AOSP dialer and search box for the removals to find.
ROM="$WORK/rom"
mkdir -p "$ROM/system/system/addon.d" "$ROM/product/app/QuickSearchBox" \
         "$ROM/system_ext/priv-app/Dialer"
cat > "$ROM/system/system/build.prop" <<'PROP'
ro.build.version.sdk=36
ro.product.brand=google
PROP
echo stale > "$ROM/product/app/QuickSearchBox/QuickSearchBox.apk"
echo stale > "$ROM/system_ext/priv-app/Dialer/Dialer.apk"

TMPDIR_I="$WORK/tmp"

# The installer removes its own temp dir on the way out, so each run gets a
# fresh copy of the installer payload.
install_run() {
  rm -rf "$TMPDIR_I"
  mkdir -p "$TMPDIR_I"
  unzip -q -o "$ZIP" 'installer/*' -d "$TMPDIR_I"
  busybox ash "$HERE/harness.sh" "$TMPDIR_I" "$ZIP" "$ROM"
}

echo "== install =="
if ! install_run > "$WORK/log" 2>&1; then
  echo "installer failed:"; cat "$WORK/log"; exit 1
fi
sed 's/^/  | /' "$WORK/log"

echo "== assertions =="
check "gapps installed to /product"    '[ -s "$ROM/product/priv-app/Velvet/Velvet.apk" ]'
check "gapps installed to /system_ext" '[ -s "$ROM/system_ext/priv-app/GoogleDialer/GoogleDialer.apk" ]'
check "permissions xml installed"      '[ -s "$ROM/product/etc/permissions/privapp-permissions-google-p.xml" ]'
check "payload size matches source"    '[ "$(stat -c%s "$ROM/product/priv-app/Velvet/Velvet.apk")" = "$(stat -c%s "$FIXTURE/assets/gsa.apk")" ]'
check "payload bytes match source"     'cmp -s "$ROM/product/priv-app/Velvet/Velvet.apk" "$FIXTURE/assets/gsa.apk"'
check "superseded QuickSearchBox gone" '[ ! -e "$ROM/product/app/QuickSearchBox" ]'
check "superseded AOSP Dialer gone"    '[ ! -e "$ROM/system_ext/priv-app/Dialer" ]'
check "build.prop got gms version"     'grep -q "^ro.com.google.gmsversion=16_202509$" "$ROM/system/system/build.prop"'
check "build.prop kept existing props" 'grep -q "^ro.product.brand=google$" "$ROM/system/system/build.prop"'
check "addon.d script installed"       '[ -x "$ROM/system/system/addon.d/69-306gapps.sh" ]'
check "install record written"         '[ -s "$ROM/system/system/etc/306gapps/files.list" ]'
check "no scratch file left behind"    '[ -z "$(find "$ROM" -name ".306gapps.part")" ]'
check "apk mode is 0644"               '[ "$(stat -c%a "$ROM/product/priv-app/Velvet/Velvet.apk")" = "644" ]'
check "symlink created, not copied"    '[ -L "$ROM/product/priv-app/PrebuiltGmsCore/lib/arm64/libjni.so" ]'
check "symlink points at its target"   '[ "$(readlink "$ROM/product/priv-app/PrebuiltGmsCore/lib/arm64/libjni.so")" = "/product/lib64/libjni.so" ]'
check "digests shipped for payloads"   'unzip -p "$ZIP" installer/digests.txt | grep -q "product/priv-app/Velvet/Velvet.apk  "'
check "empty file created, size 0"     '[ -f "$ROM/product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk.prof" ] && [ ! -s "$ROM/product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk.prof" ]'
check "no digest line for empty file"  '! unzip -p "$ZIP" installer/digests.txt | grep -q "\.prof"'
check "no digest line for the symlink" '! unzip -p "$ZIP" installer/digests.txt | grep -q libjni'
check "corrupt payload is refused"     'corrupt_payload_is_refused'

echo "== rejects a mismatched ROM =="
sed -i 's/ro.build.version.sdk=36/ro.build.version.sdk=34/' "$ROM/system/system/build.prop"
if install_run >"$WORK/log2" 2>&1; then
  echo "  FAIL installer accepted an API 34 ROM"; fail=1
else
  grep -q "API 36 but this ROM is API 34" "$WORK/log2" \
    && echo "  ok   refuses to install on the wrong Android version" \
    || { echo "  FAIL wrong error:"; cat "$WORK/log2"; fail=1; }
fi

echo "== refuses when the partition is full =="
sed -i 's/ro.build.version.sdk=34/ro.build.version.sdk=36/' "$ROM/system/system/build.prop"
if FAKE_FREE=1024 install_run >"$WORK/log3" 2>&1; then
  echo "  FAIL installer ignored a full partition"; fail=1
else
  grep -q "not enough space" "$WORK/log3" \
    && echo "  ok   aborts before writing when space is short" \
    || { echo "  FAIL wrong error:"; cat "$WORK/log3"; fail=1; }
fi

exit $fail
