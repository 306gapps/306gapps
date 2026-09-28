#!/usr/bin/env bash
# Installs a package into a fake ROM, then uninstalls it and checks that
# nothing of ours survives and nothing of the ROM's was touched.
# Usage: run.sh <306gapps-binary> <fixture-dir>
set -euo pipefail

BIN=$1
FIXTURE=$2
HERE=$(cd "$(dirname "$0")" && pwd)
HARNESS="$HERE/../installer/harness.sh"
W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT

fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }

export GAPPS_CACHE="$W/cache"
"$BIN" build -source "$FIXTURE" -packages gsa,photos,dialer-google \
  -out "$W/gapps.zip" >/dev/null 2>&1
"$BIN" uninstaller -source "$FIXTURE" -no-busybox -out "$W/uninstall.zip" >/dev/null 2>&1

ROM="$W/rom"
mkdir -p "$ROM/system/system/addon.d" "$ROM/product/app/QuickSearchBox" \
         "$ROM/system_ext/priv-app/Dialer" "$ROM/product/app/RomApp"
printf 'ro.build.version.sdk=36\n' > "$ROM/system/system/build.prop"
echo stale > "$ROM/product/app/QuickSearchBox/QuickSearchBox.apk"
echo stale > "$ROM/system_ext/priv-app/Dialer/Dialer.apk"
# A ROM file in a directory we also write into, to prove pruning stops at it.
echo rom > "$ROM/product/app/RomApp/RomApp.apk"

run_zip() {
  local zip=$1 tmp="$W/tmp"
  rm -rf "$tmp"; mkdir -p "$tmp"
  unzip -q -o "$zip" 'installer/*' -d "$tmp"
  busybox ash "$HARNESS" "$tmp" "$zip" "$ROM"
}

echo "== install =="
run_zip "$W/gapps.zip" > "$W/log1" 2>&1 || { cat "$W/log1"; exit 1; }
check "gapps installed"        '[ -s "$ROM/product/priv-app/Velvet/Velvet.apk" ]'
check "addon.d installed"      '[ -x "$ROM/system/system/addon.d/69-306gapps.sh" ]'
check "record written"         '[ -s "$ROM/system/system/etc/306gapps/files.list" ]'
INSTALLED=$(wc -l < "$ROM/system/system/etc/306gapps/files.list")
echo "  ($INSTALLED files recorded)"

echo "== uninstall =="
run_zip "$W/uninstall.zip" > "$W/log2" 2>&1 || { cat "$W/log2"; exit 1; }
sed 's/^/  | /' "$W/log2" | grep -vE '^\s*\|\s*$' | tail -6

echo "== assertions =="
check "gapps payload gone"          '[ ! -e "$ROM/product/priv-app/Velvet" ]'
check "gapps in system_ext gone"    '[ ! -e "$ROM/system_ext/priv-app/GoogleDialer" ]'
check "addon.d script gone"         '[ ! -e "$ROM/system/system/addon.d/69-306gapps.sh" ]'
check "install record gone"         '[ ! -e "$ROM/system/system/etc/306gapps" ]'
check "empty dirs pruned"           '[ ! -d "$ROM/product/priv-app/PrebuiltGmsCore" ]'
check "ROM file untouched"          '[ -s "$ROM/product/app/RomApp/RomApp.apk" ]'
check "ROM dir not pruned"          '[ -d "$ROM/product/app/RomApp" ]'
check "no 306gapps leftovers"       '[ -z "$(find "$ROM" -name "*306gapps*")" ]'

echo "== uninstalling twice is harmless =="
run_zip "$W/uninstall.zip" > "$W/log3" 2>&1 || { echo "  FAIL second run errored"; fail=1; }
grep -q "nothing to uninstall" "$W/log3" \
  && echo "  ok   reports nothing to uninstall" \
  || { echo "  FAIL wrong message:"; tail -3 "$W/log3"; fail=1; }

exit $fail
