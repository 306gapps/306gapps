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

# Read out once rather than piping each check into grep -q: under pipefail a
# grep that matches and exits early can hand unzip a SIGPIPE, which fails the
# pipeline even though the entry is there.
unzip -l "$ZIP" > "$WORK/listing"
unzip -p "$ZIP" installer/digests.txt > "$WORK/digests"

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
# Payloads are published compressed when that saves anything, so the source
# to compare against is whatever the asset decodes to. Comparing the
# installed bytes with it is also what proves the round trip is lossless.
SRC="$WORK/gsa-source.apk"
if [ -f "$FIXTURE/assets/gsa.apk.gz" ]; then
  gzip -dc "$FIXTURE/assets/gsa.apk.gz" > "$SRC"
else
  cp "$FIXTURE/assets/gsa.apk" "$SRC"
fi
check "payload size matches source"    '[ "$(stat -c%s "$ROM/product/priv-app/Velvet/Velvet.apk")" = "$(stat -c%s "$SRC")" ]'
check "payload bytes match source"     'cmp -s "$ROM/product/priv-app/Velvet/Velvet.apk" "$SRC"'
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
check "digests shipped for payloads"   'grep -q "product/priv-app/Velvet/Velvet.apk  " "$WORK/digests"'
check "empty file created, size 0"     '[ -f "$ROM/product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk.prof" ] && [ ! -s "$ROM/product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk.prof" ]'
check "no digest line for empty file"  '! grep -q "\.prof" "$WORK/digests"'
check "no digest line for the symlink" '! grep -q libjni "$WORK/digests"'
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

echo "== counts space freed by removals =="
# Same tiny free space that aborted above, but now a large superseded app is
# present for the removals to reclaim. It should install rather than abort.
mkdir -p "$ROM/product/app/QuickSearchBox"
head -c 10485760 /dev/zero > "$ROM/product/app/QuickSearchBox/big.apk" 2>/dev/null
if FAKE_FREE=204800 install_run >"$WORK/log4" 2>&1; then
  grep -q "once superseded apps go" "$WORK/log4" \
    && echo "  ok   removal-freed space counted in the check" \
    || { echo "  FAIL no reclaim shown:"; cat "$WORK/log4"; fail=1; }
  [ ! -e "$ROM/product/app/QuickSearchBox" ] \
    && echo "  ok   installed after reclaiming space" \
    || { echo "  FAIL superseded app not removed"; fail=1; }
else
  echo "  FAIL aborted though removals would free enough:"; cat "$WORK/log4"; fail=1
fi

echo "== the package is signed =="
check "MANIFEST.MF present"     'grep -q "META-INF/MANIFEST.MF" "$WORK/listing"'
check "signature block present" 'grep -qE "META-INF/[A-Z0-9]+\.RSA" "$WORK/listing"'
check "openssl verifies it"     '
  d=$(mktemp -d); unzip -q -o "$ZIP" "META-INF/*" -d "$d"
  openssl smime -verify -inform DER -in "$d"/META-INF/*.RSA \
    -content "$d"/META-INF/*.SF -noverify -binary -out /dev/null 2>/dev/null'
check "every payload is digested" '
  d=$(mktemp -d); unzip -q -o "$ZIP" META-INF/MANIFEST.MF -d "$d"
  n=$(grep -c "^Name: files/" "$d/META-INF/MANIFEST.MF")
  z=$(unzip -l "$ZIP" | grep -c " files/")
  [ "$n" = "$z" ]'

exit $fail
