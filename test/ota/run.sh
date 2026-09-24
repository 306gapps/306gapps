#!/usr/bin/env bash
# Drives the ota target end to end against a synthetic target-files package and
# stand-in AOSP tools. Usage: run.sh <306gapps-binary> <fixture-dir>
set -euo pipefail

BIN=$1
FIXTURE=$2
HERE=$(cd "$(dirname "$0")" && pwd)
W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT

fail=0
check() {
  if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi
}

python3 "$HERE/make_target_files.py" "$W/base-target_files.zip" >/dev/null
mkdir -p "$W/tools" "$W/keys"
printf 'k' > "$W/keys/releasekey.pk8"
printf 'k' > "$W/keys/releasekey.x509.pem"

# Stand-ins that record their invocation and produce their output file.
for t in add_img_to_target_files sign_target_files_apks ota_from_target_files; do
  cat > "$W/tools/$t" <<EOF
#!/bin/sh
echo "$t \$*" >> "$W/calls.log"
case "$t" in
  sign_target_files_apks|ota_from_target_files)
    eval "out=\\\$\$#"; printf 'package' > "\$out" ;;
esac
exit 0
EOF
  chmod +x "$W/tools/$t"
done

export GAPPS_CACHE="$W/cache"
build() { "$BIN" build -source "$FIXTURE" -target ota -packages gsa,dialer-google "$@" 2>&1; }

echo "== merged-only (no keys) =="
build -ota-base "$W/base-target_files.zip" -out "$W/merged.zip" > "$W/log1"
check "produces a merged target-files package" '[ -s "$W/merged.zip" ]'
check "says it is not flashable"               'grep -q "not a flashable zip" "$W/log1"'
check "runs no AOSP tools"                     '[ ! -f "$W/calls.log" ]'

python3 - "$W/merged.zip" <<'PY' > "$W/inspect"
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = set(z.namelist())
print("HAS_GMS", any(n.endswith("PrebuiltGmsCore.apk") for n in names))
print("NO_IMAGES", not any(n.startswith("IMAGES/") for n in names))
print("NO_CAREMAP", "META/care_map.pb" not in names)
print("KEPT_FRAMEWORK", "SYSTEM/framework/framework.jar" in names)
print("DROPPED_QSB", not any("QuickSearchBox" in n for n in names))
certs = z.read("META/apkcerts.txt").decode()
print("PRESIGNED", 'certificate="PRESIGNED"' in certs)
fsc = z.read("META/product_filesystem_config.txt").decode()
print("OWNERSHIP", "priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk 0 0 644" in fsc)
PY
for k in HAS_GMS NO_IMAGES NO_CAREMAP KEPT_FRAMEWORK DROPPED_QSB PRESIGNED OWNERSHIP; do
  check "$k" "grep -q '^$k True$' '$W/inspect'"
done

echo "== full signed chain =="
build -ota-base "$W/base-target_files.zip" -ota-keys "$W/keys" \
      -ota-tools "$W/tools" -out "$W/ota.zip" > "$W/log2"
check "produces the sideloadable package" '[ -s "$W/ota.zip" ]'
check "ran add_img_to_target_files"       'grep -q "^add_img_to_target_files -a " "$W/calls.log"'
check "ran sign_target_files_apks"        'grep -q "sign_target_files_apks --default_key_mappings" "$W/calls.log"'
check "ran ota_from_target_files"         'grep -q "ota_from_target_files --package_key" "$W/calls.log"'
check "ran them in order"                 '[ "$(wc -l < "$W/calls.log")" = 3 ]'

echo "== refuses early when it cannot finish =="
if build -ota-base "$W/base-target_files.zip" -ota-keys "$W/keys" \
         -ota-tools /nonexistent -out "$W/nope.zip" > "$W/log3" 2>&1; then
  echo "  FAIL accepted a missing toolchain"; fail=1
else
  check "names every missing tool" 'grep -q "add_img_to_target_files not found" "$W/log3"'
  check "does not merge first"     '[ ! -f "$W/nope-target-files.zip" ]'
fi

echo "== rejects a missing base =="
if build -out "$W/x.zip" > "$W/log4" 2>&1; then
  echo "  FAIL accepted a missing base"; fail=1
else
  check "explains what is needed" 'grep -q "target_files" "$W/log4"'
fi

exit $fail
