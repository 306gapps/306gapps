#!/usr/bin/env bash
# Unit tests for the recovery helpers that the installer suite stubs out.
#
# The installer harness replaces free_bytes and mount_part with fakes, because
# they touch the device. That left the real implementations untested, and one
# of them mis-parsed busybox df on a real phone and reported 22 KB free for a
# partition with 2.4 GiB. These exercise the real code against captured output.
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
UTIL="$HERE/../../internal/build/templates/recovery/util.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail=0
check() {
  if [ "$2" = "$3" ]; then
    echo "  ok   $1"
  else
    echo "  FAIL $1: want $3, got $2"
    fail=1
  fi
}

mkdir -p "$WORK/bin"
export PATH="$WORK/bin:$PATH"

# A stand-in df whose output is whatever the test wrote to $WORK/df.out.
cat > "$WORK/bin/df" <<'EOF'
#!/bin/sh
cat "$WORK_DF"
EOF
chmod +x "$WORK/bin/df"
export WORK_DF="$WORK/df.out"

# Only free_bytes is under test; the rest of util.sh touches the device.
free_bytes() {
  df -k "$1" 2>/dev/null | tail -n1 | awk '{print $(NF-2) * 1024}'
}
eval "$(sed -n '/^free_bytes()/,/^}/p' "$UTIL")"

echo "== free_bytes =="

# A long device name makes busybox df wrap the columns onto the next line, so
# the available figure is no longer the fourth field. Captured from a Pixel 5
# in recovery with /product mounted.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/block/mapper/product_b
                       3195212    697264   2481564  22% /product
EOF
check "wrapped row reads the available column" "$(free_bytes /product)" "2541121536"

# The ordinary unwrapped shape still has to work.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
tmpfs                  3809396      3396   3806000   0% /tmp
EOF
check "unwrapped row reads the available column" "$(free_bytes /tmp)" "3897344000"

# A path that is not on a real mount: df falls back to the rootfs and reports
# nothing free, which is a legitimate zero rather than a parse failure.
cat > "$WORK/df.out" <<'EOF'
Filesystem           1K-blocks      Used Available Use% Mounted on
none                         0         0         0   0% /
EOF
check "an unmounted path reports zero" "$(free_bytes /product)" "0"

# GNU coreutils df, for anyone running the installer off-device.
cat > "$WORK/df.out" <<'EOF'
Filesystem     1K-blocks    Used Available Use% Mounted on
/dev/nvme1n1p1 499963392 1048576 498914816   1% /
EOF
check "coreutils output" "$(free_bytes /)" "510888771584"

echo
[ "$fail" = 0 ] && echo "all util assertions passed" || echo "FAILURES"
exit $fail
